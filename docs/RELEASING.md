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
workflow_dispatch     ──►  osac-release.yaml         ──┘        (prepare → build → test → publish → tag)
```

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
push/tag-triggered build workflow (`build-image.yaml`,
`publish-image.yaml`, `build-bmf-image.yaml`, `execution-environment.yml`,
`build-metering-*-image.yaml`), and `publish-charts.yaml` publishes that
component's chart once its image build completes. This path is unrelated
to nightly/release and always available — see "Releasing a new component
version" below.

## Versioning model

There is no single version number stamped onto everything. Each
mono-repo component keeps its own independent version
(`<component>/vX.Y.Z` git tags); only the umbrella chart (`osac`) gets its
own release version. "OSAC vX.Y.Z" means a specific, tested combination of
component versions — recorded in the published chart's `Chart.lock` and in
that run's release notes/Slack summary, not implied by the number itself.

`osac-ui` is an exception: it lives in its own repo
(`osac-project/osac-ui`) with its own independent release cadence, and is
never built by this pipeline — only its already-published image/chart are
referenced. Bumping it means releasing it in its own repo first.

## How nightlies work

Every night at 03:00 UTC (or via a manual `workflow_dispatch` on
`nightly-build.yaml`, with an optional `skip_e2e` input):

1. Every mono-repo component is rebuilt fresh from `HEAD`, whether or not
   it changed since its last real release.
2. Each gets tagged `<latest-real-base>-nightly.<date>.<sha>.<run>.<attempt>`
   — never a permanent tag, never a bare `<component>/vX.Y.Z`.
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

**Required input:** `release_version` — a real, human-chosen semver (e.g.
`0.1.0`, no leading `v`). The run fails immediately (in the `prepare` job,
before anything builds) if `osac/v<release_version>` already exists — you
cannot re-release or silently overwrite a version that already shipped;
cut the next patch instead.

**Default behavior (no other input needed):** every component is pinned
to its current latest real `<component>/vX.Y.Z` tag, verbatim — no
rebuild, no retag. Only the umbrella chart is new. This is the common
case: "package whatever's already released into a new umbrella cut."

**Optional `component_versions` input** — bump one or more components as
part of the same release, as comma-separated `component=version` pairs:

```
osac-operator=0.0.13,fulfillment-service=0.0.99
```

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
- `osac/v<release_version>` — a real, permanent git tag.
- A real `<component>/vX.Y.Z` tag for every component that was actually
  bumped this run.
- The published umbrella chart's `Chart.lock` resolves every dependency
  to its real, already-published OCI version — no `file://` paths, no
  nightly suffixes anywhere.
- Every image and chart signed with cosign; see the root
  [`README.md`](../README.md#verifying-container-image-signatures) for
  how to verify a specific artifact.

## Releasing a new component version

Two ways to get a new component version out, depending on what you need:

1. **Just release the component, independent of any OSAC release.** Push
   a `<component>/vX.Y.Z` tag directly (e.g. `git tag
   osac-operator/v0.0.13 && git push origin osac-operator/v0.0.13`). That
   component's own build workflow triggers on the tag push;
   `publish-charts.yaml` picks up the completed build and publishes its
   chart. This makes the version available in GHCR, but does **not** pull
   it into a new umbrella release — `osac`'s currently-published version
   keeps pointing at whatever it already pinned until a release run picks
   up the new tag.
2. **Release the component as part of a new OSAC version, in one
   dispatch.** Use `osac-release.yaml`'s `component_versions` input (see
   above) — it builds, tests, tags, and publishes the component *and*
   bundles it into the new umbrella release together, atomically.

Use (1) when you just need the component's own artifact out (e.g. someone
else is consuming the chart/image directly) without cutting a full OSAC
release. Use (2) when the point is to ship that fix as part of the next
OSAC version.

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
