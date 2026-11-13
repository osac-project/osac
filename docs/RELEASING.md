# Releasing

This document covers the current release mechanism for OSAC: how nightly
builds work, how to cut a real `osac` release, and how to release a new
version of a single component.

## Architecture

One shared, `workflow_call`-only pipeline does the actual work; two thin
workflows trigger it:

```
schedule (03:00 UTC)  ──►  nightly-build.yaml       ──┐
                                                          ├──►  osac-build-and-publish.yaml
workflow_dispatch     ──►  osac-release.yaml         ──┘        (prepare → build → test →
                                                                  promote-and-publish → publish-umbrella →
                                                                  tag → notify-release)
```

In release mode, `tag` also fans out to three more jobs —
`create-component-releases`, `publish-fulfillment-service-binaries`, and
`publish-fulfillment-service-proto`. After those finish,
`create-umbrella-release` publishes the OSAC chart's GitHub Release and
marks the highest stable umbrella version as the repository's Latest release.
Prereleases are explicitly marked and never become Latest. `notify-release`
waits for all four jobs before announcing **GHCR publication**. This message
does not claim that Konflux has finished: a separate `notify-konflux` job
waits for the image and chart staging jobs and reports their final outcomes.
See "What you get on success" below for the release pages and artifacts.

Slack lists the umbrella chart first, followed by every bundled subchart
version from the exact published package and `Chart.lock`, including reused
versions and CRD charts. Local fallbacks and missing lock entries are labeled.
The standalone CSI charts are listed separately because the umbrella does not
install them. This inventory is independent of which components were newly
published during the run.

- **`osac-build-and-publish.yaml`** — the real pipeline: resolves every
  component's version, builds and tests whatever needs building, packages
  and signs charts/images, and tags. Never runs on its own; always invoked
  via `workflow_call` with a `mode: nightly | release` input.
- **`nightly-build.yaml`** — schedule trigger (plus an ad-hoc manual
  `workflow_dispatch` with a `skip_e2e` option). Calls the pipeline with
  `mode: nightly`.
- **`osac-release.yaml`** — `workflow_dispatch`-only. Calls the pipeline
  with `mode: release`, `release_version` (required), and an optional
  `component_versions` input.

Independently of both, each mono-repo component still has its own
push/pull_request-triggered build workflow (`build-image.yaml`,
`publish-image.yaml`, `build-bmf-image.yaml`, `execution-environment.yml`,
`build-metering-*-image.yaml`), but only for a `main`-branch push (a
`sha-<short>`-tagged dev image) or a pull request (build-only, never
pushed or signed). None of them react to a `<component>/vX.Y.Z` tag push
anymore — `osac-build-and-publish.yaml` is the sole source of truth for
producing anything under a real version: image, chart, and (for
fulfillment-service) binaries and the proto schema. See "Releasing a new
component version" below.

## Versioning model

Every branch records its **release line** (`X.Y`) in the `RELEASE_LINE` file
at the repository root: `main` holds the upcoming release, `release-X.Y`
holds `X.Y`. Everything built from a branch shares that `X.Y`. Inside the
line, each mono-repo component (`<component>/v…` git tags) and the umbrella
chart (`osac/v…`) keep their own patch number and their own candidate
counter, so releasing one component never renumbers another.

A release version is `X.Y.Z` or `X.Y.Z-rc.N`, nothing else:

| Stage | Version | Built from |
|---|---|---|
| Nightly | `X.Y.Z-nightly.<date>.<sha>.<run>.<attempt>` | `main`, automatic |
| Release candidate | `X.Y.Z-rc.N` | `release-X.Y` |
| Stable | `X.Y.Z` | `release-X.Y` |

Semver precedence follows that order (`nightly` < `rc` < stable), and the
pipeline enforces the maturity it implies:

- A candidate umbrella can only build candidate component versions, so a
  stable-looking component version never exists before it ships in a stable
  umbrella.
- A stable umbrella can only contain stable component versions. The first
  stable release of a line therefore releases every component as `X.Y.0`.
- A new version must be higher than the latest one in its line, and no
  version outside the branch's line is accepted. Git tags are not
  branch-scoped; the line check is what keeps `release-0.4` and `main` from
  resolving or minting each other's versions.

"OSAC vX.Y.Z" means a specific, tested combination of component versions —
recorded in the published chart's `Chart.lock` and in that run's release
notes/Slack summary, not implied by the number itself.

`osac-ui` is a mono-repo component like every other one here — built,
tested, tagged, and published by this same pipeline, with its own
`osac-ui/vX.Y.Z` tags. It has its own unit-test job but no integration-test
suite, and never gets a GitHub Release page (`fulfillment-service` does,
just with attached binaries rather than a plain changelog — see "What you
get on success" below), but otherwise follows the exact same version model
as the rest.

## How nightlies work

Every night at 03:00 UTC (or via a manual `workflow_dispatch` on
`nightly-build.yaml`, with an optional `skip_e2e` input):

1. Every mono-repo component is rebuilt fresh from `HEAD`, whether or not
   it changed since its last real release.
2. Each is versioned `<base>-nightly.<date>.<sha>.<run>.<attempt>` — never a
   permanent tag, never a bare `<component>/vX.Y.Z`. `<base>` is `X.Y.0` of
   the branch's release line, or the patch after the line's latest stable
   version of that component once one exists.
3. The full test gate runs: unit, integration, security (CodeQL), and the
   three e2e suites (vmaas/caas/bmaas) unless `skip_e2e` was set.
4. On success, every image and the umbrella chart are signed with cosign
   and published; the umbrella gets tagged
   `osac/v<base>-nightly.<date>.<sha>.<run>.<attempt>` — also throwaway,
   never a clean `osac/vX.Y.Z`.

Nightly never creates a permanent component tag. It exists to catch
integration breakage early, not to produce a shippable artifact.

## Releasing a new OSAC version

Dispatch `osac-release.yaml` (Actions tab → "OSAC Release" → "Run
workflow", or `gh workflow run osac-release.yaml --ref main -f
release_version=<version>`), selecting the branch/ref you want to release
from GitHub's own "Use workflow from" selector — there is no separate
ref input.

**Required input:** `release_version` — `X.Y.Z` or `X.Y.Z-rc.N` in the
dispatched branch's release line (e.g. `0.5.0-rc.2`, no leading `v`), higher
than the line's latest umbrella version. The run fails immediately (in the `prepare` job,
before anything builds) if `osac/v<release_version>` already exists — you
cannot re-release or silently overwrite a version that already shipped;
cut the next patch instead.

**Default behavior (no other input needed):** every component is pinned
to its latest version in the branch's release line (candidates included),
verbatim — no rebuild, no retag. A component with no version in the line
yet must be named in `component_versions`; the run fails otherwise. Only the umbrella chart is new. This is the common
case: "package whatever's already released into a new umbrella cut."

**Optional `component_versions` input** — bump one or more components as
part of the same release, as comma-separated `component=version` pairs:

```
osac-operator=0.5.0-rc.2,fulfillment-service=0.5.0-rc.3
```

A named version is either new or the component's existing latest:

- A **new** version must be in the branch's line, higher than that
  component's latest, and a candidate exactly when `release_version` is one.
- An **existing** version is only accepted when it is the component's
  latest in the line, which is what the default already pins. A candidate
  umbrella may pin a stable version this way.

An older version cannot be pinned. The Konflux chart pipelines publish each
chart's highest version in the line, so a release always carries every
component's latest. For a stable release this means a component whose
latest version is a candidate must get a stable version in the same run.

For each named component:
- If that exact version already exists as a real tag, it's just pinned
  (no redundant rebuild).
- If it doesn't exist yet, the component is built fresh from the
  dispatched ref, run through the full test gate, and — **only if
  everything in the run passes** — tagged as a real, permanent
  `<component>/vX.Y.Z` git tag and published at that version. A failure
  anywhere in the run (including in a component you didn't ask to bump)
  means no permanent artifact gets published: no promoted image at its
  final version, no published chart, no permanent git tag. This applies
  to final artifacts only — a failed run can still leave provisional
  `sha-<short>` images in GHCR from whatever the `build` job already
  pushed before the failure; those were never promoted or tagged, and a
  future run isn't affected by their presence.

Any component not named in `component_versions` uses the default
(pin-to-latest) behavior regardless.

**Optional `skip_e2e` input** — skips the three e2e suites. Reserve this
for validating the release *mechanism* itself (e.g. after a change to
`osac-build-and-publish.yaml`); a real release should run the full gate.

**What you get on success:**
- `osac/v<release_version>` — a real, permanent git tag and a GitHub
  Release titled `OSAC Platform Chart v<release_version>`. This release is
  eligible for Latest only when it is stable and no higher stable umbrella
  release already exists. Component releases and the floating graph release
  explicitly opt out of Latest, including binary releases at creation time.
  Its notes link to the [published package](https://github.com/orgs/osac-project/packages/container/package/charts%2Fosac)
  and retain `oci://ghcr.io/osac-project/charts/osac` for Helm.
  The dependency inventory comes from the exact published package's chart
  metadata and `Chart.lock`, including reused versions and both operator CRD
  charts. A local dependency fallback or missing lock entry is explicitly
  labeled, not silently omitted or described as a published OCI dependency.
  The inventory is also attached as `umbrella-dependencies.json`.
  Separately published `csi-driver` and `csi-backends` charts are listed in
  their own section: neither is installed by the umbrella chart.
  Notes include an exact version-pinned install command and a link to the
  [customer installation guide](guides/installation/customer-install-guide.md).
  Complete its prerequisites and prepare your values before installing.
- A real `<component>/vX.Y.Z` tag for every component that was actually
  bumped this run.
- The published umbrella chart's `Chart.lock` records its resolved dependencies.
  Every mono-repo component has a resolved version in a release run (the run
  fails otherwise), so each is a pinned OCI dependency.
- Every image and chart signed with cosign; see the root
  [`README.md`](../README.md#verifying-container-image-signatures) for
  how to verify a specific artifact.
- A plain changelog-style GitHub Release page for every bumped component
  among `osac-operator`, `osac-aap`, `bare-metal-fulfillment-operator`,
  `osac-csi-driver`, and `osac-metering` (`osac-ui` never gets one, on any
  release). These component releases remain separate from the umbrella
  chart release and are titled with their component names.
- If `fulfillment-service` was bumped this run: its own GitHub Release
  page too, but a different kind — created by goreleaser with its signed
  release binaries attached, not the plain changelog style above — plus
  its proto schema pushed to the buf.build
  registry.
- A real **staging release** to Red Hat's registry — see "Staging release
  (Konflux)" below.

## Release branches and code freeze

For a release that needs a stabilization period instead of shipping
straight off `main`, cut a release branch named `release-X.Y`, where `X.Y`
is `main`'s current `RELEASE_LINE` (e.g. `release-0.5` while `main` is at
`0.5`), and push it directly to `upstream` — not a fork, since
every contributor opens PRs against it for the duration of the freeze,
the same way they would against `main`:

```bash
git fetch upstream
git checkout -b release-0.5 upstream/main
git push upstream release-0.5
```

The new branch keeps the `RELEASE_LINE` it was cut with. Immediately open a
PR on `main` that bumps `RELEASE_LINE` to the next line (`0.5` → `0.6`), so
nightlies start tracking the next release and the two branches can never
mint versions in the same line.

GitHub's required checks (`CI`, CodeQL, `jira-reference`, Component Hooks
Smoke) and Konflux's build/Enterprise-Contract triggers both already
match any `release-*`-named branch the same way they match `main` — this
was made generic once, so no workflow/`.tekton` changes are needed per
release branch. `osac-release.yaml` itself needs no `ref` input either:
dispatch it against the release branch directly via GitHub's own "Use
workflow from" selector or `--ref`.

**Landing fixes during the freeze:** land every fix on `main` first via a
normal PR, then cherry-pick that commit onto the release branch via a
second PR targeting it (`git cherry-pick -x <sha>`, same review bar and
required checks). This direction — forward from `main`, never backward —
keeps `main` from falling behind a short-lived branch and avoids a later
back-merge silently reintroducing an already-superseded fix. Resolve
cherry-pick conflicts by hand rather than dropping the fix.

**Branch protection:** ask whoever has GitHub org admin access to add a
branch-protection rule for `release-X.Y` (or a `release-*` wildcard rule,
if the org's UI supports one) mirroring `main`'s current rule — same
required approvals, same required status checks, no force-push/delete —
*before* the first PR lands on it.

**Dispatching the release** at the end of the freeze is the same
`osac-release.yaml` dispatch as above, just with `--ref release-X.Y`
instead of `main`.

**Cleanup:** delete the release branch a few days after the release ships
and no hotfix need has surfaced — the authoritative record of what
shipped is the `osac/vX.Y.Z` tag and the published artifacts, not the
branch. If a patch release (e.g. `0.5.1`) becomes necessary before then,
dispatch it from the still-live `release-0.5` branch directly rather than
re-deriving a new branch from the tag.

## Staging release (Konflux)

Every release run also triggers a separate, independent build through
Konflux, Red Hat's internal build/release system. Only what the run itself
builds is released through it:

- **Images** of the components bumped in this run, out of the nine Konflux
  image components (`fulfillment-service`, `osac-operator`, `osac-aap`,
  `osac-csi-driver`, `bare-metal-fulfillment-operator`, `osac-ui`, and
  `osac-metering`'s `metering-service`, `metering-m360-adapter` and
  `metering-echo-adapter`).
- **Charts** of those same components, plus the umbrella chart, which is
  new in every release.

`trigger-konflux-staging-release` waits for those builds to finish and pass
their policy gates, then releases them together to the **staging** registry
(`registry.stage.redhat.io`, under `cloud-extensions/<component>-rhel10`).
A release that bumps no component creates no image Snapshot; the Slack
message then gives `none` as the image Snapshot, which is what
`osac-promote-to-prod.yaml`'s `image_snapshot` input expects in that case.

A version is never built twice, so a pinned version must already be on the
registry, and its tag alone does not prove that: a Konflux build or a
staging release can fail and leave the version tags behind. Each staging job
therefore records what it released with a `konflux-staged/<name>-v<version>`
tag (image and chart component names). A release that pins a version with no
such record stops in `prepare` and lists it. Either release a new version of
that component, or, if the version is confirmed on the staging registry,
push the missing `konflux-staged/` tag by hand. Versions released before
this record existed need that tag pushed once.

A pinned component is not released again: its version is already on the
registry from the release that built it. Konflux still starts a build for
every component on the release tag, but a pinned component's rebuild has no
version of its own and would be published as `dev`, and a pinned chart
would be pushed again over its released version, so neither is released.
This means a pinned version is in production only if the release that built
it was promoted — promote stable releases in order.

A separate, manually-dispatched workflow, `osac-promote-to-prod.yaml`,
promotes an already-staged release to `registry.redhat.io` once it's
ready. Images promote with no rebuild — the same built digest is
multi-homed into `registry.redhat.io` by `cloud-extensions-prod`'s own
`ReleasePlanAdmission`. The chart cannot: its own `values.yaml` has
`registry.stage.redhat.io` baked into every component image reference at
build time, so the already-built staging chart is only ever correct for
stage. Promoting it re-stamps that exact same source commit with
`registry.redhat.io` (only the hostname changes) and pushes it through a
real, independent Konflux build — expect the chart side of a production
promotion to take meaningfully longer than the image side as a result.

A component only reaches staging if its Konflux build passes Red Hat's
Enterprise Contract policy — a set of supply-chain and security checks
every component must satisfy. The one check worth knowing about
directly: a clean **Snyk Code (SAST)** scan, which requires a
`snyk-secret` (key `snyk_token`) to already exist in the `osac-tenant`
namespace on the Konflux cluster — without it, the scan is skipped
outright and the policy fails for that reason alone. The rest of the
policy (hermetic build integrity, SBOM/signature requirements, and so on)
is Konflux's own concern and isn't something this repo's release process
needs to reason about directly.

On success, each component's image is live on `registry.stage.redhat.io`,
signed and with an SBOM attached, and gets a real Pyxis catalog entry
(`catalog.stage.redhat.com/software/containers/cloud-extensions/<component>-rhel10`)
plus a stage errata advisory. The GHCR-side publish and the Konflux
staging release are independent — a failure in one doesn't undo the
other.

Release runs send two Slack notifications:

1. **GHCR published** after the GHCR artifacts and GitHub Release are ready,
   with the complete chart inventory and a notice that Konflux is pending.
2. A **final Konflux status** after image builds, image staging, chart
   stamping/builds, and chart staging finish or are skipped. It says
   **Complete** only when both image and chart staging succeeded. A chart
   staging failure or skip is reported explicitly even though those jobs
   currently use `continue-on-error`. Skipped validation gates remain visible
   in the final Complete message.

Nightlies send only the GHCR notification because they do not run Konflux.
Cancellation prevents new notifications from starting. Re-running a
notification job sends another message for the same run.

## Release retries and upgrades

Retrying the umbrella release job uses the already-published package and edits
an existing release instead of treating every create failure as an existing
release. It refreshes the same notes and inventory asset without moving tags.
API, missing-tag, or asset-publication failures still fail the job. This does
not make a new full dispatch for an already-published version valid: the
existing immutable-version guard in `prepare` remains in place.

Release-job retries cannot move Latest back to an older stable umbrella.
Nightly mode creates no GitHub Release; a release-mode version with a
prerelease suffix, including a nightly suffix, is marked prerelease and
explicitly excluded from Latest. Cancellation prevents the umbrella release
and success notification jobs from starting; a release publication failure
reaches the failure notification instead of a success message. Optional
Konflux staging remains independent of this GHCR publication path.

**GitHub's "Re-run failed jobs" does not recover a confirmed staging
failure.** GHCR image and chart publication (`promote-and-publish`,
`publish-umbrella`) runs before Konflux staging even starts, so those
artifacts can remain published after a confirmed staging failure — only
the GitHub Releases (component and umbrella) and fulfillment-service's
binaries/proto wait for the real Konflux image staging release to have
actually succeeded, never on a cancelled or timed-out run, only on a
confirmed failure of `trigger-konflux-staging-release` itself. Rolling
tags back after a
confirmed failure, combined with `cleanup` always deleting the temp branch
once `tag` succeeds, means a "Re-run failed jobs" attempt afterward has
neither the tags `create-component-releases` would reference nor the
branch `stamp-and-release-charts` would need to check out. Dispatch a fresh
`osac-release.yaml` run with a new `release_version` instead — don't retry
a release whose staging has already confirmed failed. A cancelled or
timed-out staging attempt is left alone on purpose (the underlying Konflux
Release it created may still be progressing and could still succeed) and
needs manual reconciliation: check whether that Release actually completed
before deciding to delete anything by hand.

The version inventory is not an upgrade compatibility guarantee. Before an
upgrade, compare the installed and target inventories, read the affected
components' release notes and installation guide, back up persistent data and
configuration, and check CRD migrations and required values changes. Do not
assume Helm upgrades CRDs automatically. Maintainers should add verified
release-specific upgrade or migration notes when applicable; do not invent
an upgrade path from version numbers alone.

Release automation changes have offline regression coverage in the required
pre-commit CI job. Run the same tests locally without publishing anything:

```sh
uv run --no-project --with pyyaml==6.0.3 python -m unittest discover -s .github/scripts -p test_umbrella_release.py -v
```

## Releasing a new component version

Use `osac-release.yaml`'s `component_versions` input (see above) — it
builds, tests, tags, and publishes the component's image *and* chart,
bundled into the new umbrella release in the same release pipeline run.

There used to be a second path — pushing a `<component>/vX.Y.Z` tag
directly, which independently triggered that component's own build
workflow (image, and for fulfillment-service also binaries and the proto
schema) plus a separate `publish-charts.yaml` workflow for its chart. Both
were removed: they duplicated `osac-build-and-publish.yaml`'s own
build/package/sign work for every tag a real release produced, and raced
to publish the same artifacts. Confirmed live on a real release: the loser
of that race ended up publishing an artifact that was never actually
tested by that release's own test gate.

`osac-build-and-publish.yaml` is now the *only* thing that can ever
produce anything under a real `<component>/vX.Y.Z` version — image,
chart, and (for fulfillment-service) binaries and the proto schema. A
manually pushed `<component>/vX.Y.Z` tag doesn't trigger anything at all
anymore; a component's own build workflow still exists, but only reacts
to a push to `main` (a `sha-<short>`-tagged dev image) or a pull request
(build-only, never pushed or signed). A nightly run publishes every
artifact at a throwaway nightly-suffixed version; a real `osac-release.yaml`
dispatch is the only way to publish one at a real, permanent
`<component>/vX.Y.Z`-matching version.

## Verifying what shipped in a release

Pull the umbrella chart and read its lockfile — it's the authoritative
record of exactly which component versions are bundled:

```bash
helm pull oci://ghcr.io/osac-project/charts/osac --version <version> --untar
cat osac/Chart.lock
```

For signature verification, see the root
[`README.md`](../README.md#verifying-container-image-signatures) and
[`README.md`](../README.md#verifying-helm-chart-signatures).
