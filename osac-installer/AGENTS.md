# OSAC Installer

Helm-based deployment orchestrator for OSAC components. No Go code, no builds, no unit tests — only structural validation.

Helm-based deployment system for the OSAC platform in the `osac` mono-repo.
osac-operator, fulfillment-service, osac-aap, bare-metal-fulfillment-operator,
osac-csi-driver, and osac-metering are sibling directories at the repository
root (referenced via `file://` in `charts/osac/Chart.yaml`). **osac-ui** is an
external OCI chart dependency. Deployment uses three Helm charts in sequence:
`charts/osac-deps/` (Phase 1), `charts/osac-infra/` (Phase 2),
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

# Full install (infra + osac)
make install PLATFORM=openshift PROFILE=<profile> NS=<namespace>

# Individual phases
make install-infra PLATFORM=openshift PROFILE=<profile> NS=<namespace>
make install-osac  PLATFORM=openshift PROFILE=<profile> NS=<namespace>

# Uninstall
make uninstall PLATFORM=openshift PROFILE=<profile> NS=<namespace>

# Integration tests (Kind)
make test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment

# Full local dev environment on Kind (superset of dev: KubeVirt + AWX + UI +
# seeded catalog). Needs a rootful runtime + /dev/kvm — see README.md and
# scripts/dev-full/. Adds the install-devstack phase after infra + osac.
make install PLATFORM=kind PROFILE=dev-full NS=osac
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
charts/osac-deps/      # Phase 1: CRD providers (OLM subscriptions on OpenShift)
charts/osac-infra/     # Phase 2: Shared infrastructure (CA, Keycloak, Gateway, PostgreSQL)
values/<profile>/      # Per-profile values (dev, vmaas-ci, bmaas-ci, caas-ci, full-ci)
prerequisites/         # Reference manifests for manual prerequisite installation
scripts/               # Automation scripts (see README.md for full list)
```

### Helm Charts (Three-Phase Deployment)

```text
Phase 1: charts/osac-deps/               # CRD providers
  Installs: cert-manager, AAP, LVMS, CNV, MCE, MetalLB
  Hook scripts wait for operators to be ready before proceeding

Phase 2: charts/osac-infra/             # Shared infrastructure
  Configures: certificates (CA issuer, trust-manager), Keycloak,
  operator CRs (HyperConverged, LVMCluster, MetalLB, MCE),
  shared PostgreSQL (dev/CI)
  Hook scripts configure each operator after its CRD is ready

Phase 3: charts/osac/                   # OSAC platform (per-instance workload)
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
  Templates: hub-access, hooks (create-hub, pre-install-validate,
    publish-templates, seed-cluster-versions, register-local-storage)
  values.schema.json validates all configuration
```

### Values Environments

```text
values/
  dev/infra.yaml + instance.yaml       # Local dev (Kind + OpenShift)
  dev/kind-infra.yaml + kind-instance.yaml       # Kind control plane (PROFILE=dev, dev-full)
  dev/kind-instance-devfull.yaml                 # PROFILE=dev-full overlay (noop networking provisioning)
  vmaas-ci/infra.yaml + instance.yaml  # VMaaS CI
  caas-ci/infra.yaml + instance.yaml   # CaaS CI
  bmaas-ci/infra.yaml + instance.yaml  # BMaaS CI
  full-ci/infra.yaml + instance.yaml   # Full CI (all components)
```

`PROFILE=dev-full` (kind only) reuses the `dev` kind control-plane values and adds
the `kind-instance-devfull.yaml` overlay; the extra dev stack (KubeVirt, AWX, UI,
seeded catalog, and a ready-to-use `tenant1`) is installed imperatively by
`scripts/dev-full/` (the `install-devstack` phase), not by the charts. The final
step, `provision-tenant.sh`, creates the DB tenant via the gRPC-only Tenants API
(creating it auto-provisions the tenant's default network via tenant onboarding),
creates the matching Keycloak organization, and adds `tenant1_user`/`tenant1_admin`
as members so they can log in and manage resources. See README.md.

Pull secrets and AAP license files are stored alongside values files (e.g.,
`values/<profile>/pull-secret.json`, `values/<profile>/license.zip`).

osac-operator, fulfillment-service, osac-aap, bare-metal-fulfillment-operator,
osac-csi-driver, and osac-metering are all mono-repo-resident directories
checked out at the repository root, not submodules -- they share this repo's
own commit history with osac-installer itself (there are no submodules under
`base/` any longer).
There is deliberately no image-tag pinning/syncing in `values/*/values.yaml` for
fulfillment-service, osac-operator, osac-aap, bare-metal-fulfillment-operator,
osac-csi-driver, and osac-metering: CI values files use the live tag published
by each component's own workflow -- `main` for fulfillment-service (the only
one of the six that doesn't publish a current `latest`) and `latest` for
osac-operator, osac-aap, bare-metal-fulfillment-operator, osac-csi-driver,
and osac-metering. osac-metering follows the standard `<component>/vX.Y.Z` tag
rule via `resolve_release_tag()` but has no release tag yet -- nightly sub-chart
OCI publish is skipped with a warning until the first tag is cut (same as
osac-csi-driver today). There is no separate commit/tag to keep in sync and no
bump-bot involved.

Prerequisites are installed via `make install-infra`, which handles both
osac-deps and osac-infra charts, gated by values toggles. `ca-bundle` Bundle is
cluster-scoped and managed by the `osac-infra` chart via trust-manager. See
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
- **nightly-charts.sh** -- Nightly chart manifest + Slack helpers (`check_osac_ui_image`, `append_chart_source`, `build_slack_charts_published_summary`, `stamp_osac_ui_chart`, `stamp_component_image_refs`, `stamp_umbrella_nested_field`, `stamp_ci_overlay_if_present`, `retag_component_image`, `rewrite_umbrella_osac_ui_dependency`, `rewrite_umbrella_osac_ui_dependency_and_rebuild`)

### CI Workflows

GitHub Actions only discovers workflows under the repo root's `.github/workflows/`,
so osac-installer-specific CI now lives there (not under `osac-installer/.github/`):
`nightly-build.yaml` (scheduled nightly + manual dispatch) has five phases:
`prepare` resolves every mono-repo component's nightly version + gates
osac-ui@main on an existing `ghcr.io/.../osac-ui:sha-<7>` image, writes
`pinned/osac-ui.commit`, stamps every Chart.yaml to its final version and
every values.yaml image-tag field to a *provisional* `sha-<short>` tag
(umbrella + sub-charts + the 4 CI overlay files), and pushes a temp branch;
`build` (a 8-way matrix) freshly builds and pushes each mono-repo component's
own image under that same provisional `sha-<short>` tag via its own `make
image-build`/`image-push` target (osac-ui has no entry -- it's external,
already built by its own CI); `unit-tests`/`integration-tests`/`security-tests`
and `e2e-{vmaas,caas,bmaas}-test` all run in parallel against the temp branch
and the just-built provisional images; `publish` (gated on every one of those
passing) promotes each image from its provisional tag to its final nightly
version via a server-side `skopeo copy` (`retag_component_image` -- no
rebuild), re-stamps the same values.yaml fields to that final version
(`stamp_component_image_refs`), then packages/pushes every chart to GHCR
(`images.txt` lists the rendered final refs); `tag-and-notify` tags
`osac/v<version>` and Slack-lists `chart-sources.txt`. A failed nightly run
therefore never leaves a real, release-looking version tag on an
unvalidated image -- only an ordinary `sha-<short>` tag indistinguishable
from any other day's regular commit build.
Also see `publish-osac-installer-chart.yaml` (manual-dispatch umbrella chart
release; takes one mono-repo release `version` plus an independent
`ui_version` for osac-ui).
Nightly sub-chart OCI publishing covers osac-operator (operator +
operator-crds), fulfillment-service, osac-aap, bare-metal-fulfillment-operator
(+ crds), osac-metering (service + m360Adapter + echoAdapter images),
osac-csi-driver (csi-driver + csi-backends), external osac-ui, and the
umbrella chart. Baseline semver uses `resolve_release_tag()` per mono-repo
component; components without a `<component>/vX.Y.Z` tag yet are skipped
(no image build, no chart publish) until their first release tag is cut.
osac-ui uses `resolve_bare_release_tag_at()` on the pinned commit.

**osac-ui nightly source strategy** (external repo; see `nightly-build.yaml`
`prepare` and `Resolve and gate osac-ui SHA` steps):

| Step | Behavior |
|------|----------|
| `check_osac_ui_image()` | `ls-remote osac-ui@main` → verify `ghcr.io/.../osac-ui:sha-<7>` manifest exists (HTTP 200); fail if image not published yet |
| Pin once per run | Full SHA written to `pinned/osac-ui.commit` on temp branch; publish checks out that exact commit |
| Baseline semver | `resolve_bare_release_tag_at()` uses `git describe` on the pinned SHA (nearest ancestor `vX.Y.Z`) |
| Umbrella image (provisional) | `prepare` sets `ui.images.ui` to `ghcr.io/.../osac-ui:sha-<7>` on umbrella + CI values -- the same image its own CI already published, no promotion yet |
| Umbrella image (final) | `publish` retags that image to the final `UI_SUB_VERSION` (`retag_component_image`) and re-points `ui.images.ui` at it, only after every test box passes |
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
