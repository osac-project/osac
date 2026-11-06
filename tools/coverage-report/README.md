# Coverage report

Generate a per-feature **Feature x Unit / Integration / E2E** table from the
repository, with gaps and a next action. The report keeps measured coverage and
test-plan mappings separate and never shows missing evidence as `0%`.

Requirements: Python 3.9+ and PyYAML. No other dependencies.

## Usage

```sh
# Every feature
tools/coverage-report/coverage-report

# One feature, measured against a Go coverprofile
tools/coverage-report/coverage-report OSAC-5731 --coverprofile /tmp/small.out

# Merge several profiles (statement blocks are deduplicated, max count wins)
tools/coverage-report/coverage-report OSAC-2540 \
  --coverprofile /tmp/servers.out --coverprofile /tmp/small.out
```

The launcher accepts the full flag set:

```sh
tools/coverage-report/coverage-report [FEATURE] \
  [--features PATH]... [--group WG] \
  [--coverprofile PATH]... [--allow-external-root PATH]... \
  [--out DIR] [--repo PATH]
```

`FEATURE` is a Jira key, name, or alias from the area config, case-insensitive.
Omitting it reports every area. `--group` limits the run to one working group.
An unknown value exits `2` and prints close matches. Output defaults to
`.artifacts/coverage-report/<KEY, GROUP, or ALL>/` and contains `report.json`
and `report.md`. Both come from the same dictionary, so the Markdown never
computes a number. Output has no timestamps; the report records
`git rev-parse HEAD`, and reruns are byte-identical.

## Configuration

Area config lives in the `org-pulse-data` repository at
`config/coverage/<wg>.yaml`, edited by MR. The `tools/coverage-report/features.yaml`
file in this repository is the Core example and remains the default so local
runs keep working. `--features` accepts a file or a directory of `*.yaml` files
and merges them; duplicate area keys are rejected. A file may set a top-level
`group:` default for its areas.

```yaml
features:
  - key: iam
    name: Identity & Access Management
    group: core
    testplans: []          # or a list of plan paths
    behaviors:             # optional inline cases, same evidence rules
      - {id: flow-tenant-update, title: Tenant Update, tier: e2e}
    unit_packages:
      - package: github.com/osac-project/osac/fulfillment-service/internal/auth
        rationale: authentication middleware and token handling
    # Optional. Only a valid reviewer handle makes these declarations count.
    mappings_reviewed_by: "@reviewer"      # or org/team
    mappings:
      - behavior: TC-FR1-01
        test: fulfillment-service/it/it_test.go
        tier: integration
```

`unit_packages` are Go import paths. Each needs a one-line rationale so a
reader can judge whether the package is really feature-specific. `group`
labels a working group in the report header. When a feature has several
`testplans`, behavior ids are prefixed with the plan's Jira key or directory
(for example `OSAC-2476/TC-FR1-01`) so they stay unique.

## Evidence model

| Class | Source | Counted? |
|---|---|---|
| measured | statements in a `--coverprofile` | yes, unit only |
| declared | a non-"proposed" path in the test plan (Execution bullet or Planning Evidence Matrix) that exists, or a reviewed `mappings:` entry | yes |
| inferred | a `mappings:` entry without a valid `mappings_reviewed_by` | listed, never counted |
| gap | no existing test, a proposed/missing path, no path, or no tier | drives the next action |

A symlinked `enhancement-proposals` checkout and `--allow-external-root` paths
may supply test-plan files from outside the repository. Only test-plan paths
are checked for containment; coverprofiles and `--features` files may live
anywhere.

A behavior is a `#### TC-...: title` (or `###`) heading. Its priority and
automation come from the `| ... | Priority | Automation |` table that follows
it. Its tier comes from the `- **Tier:**` bullet, or the legacy
`- **Tier / owner:**` bullet whose owner part is ignored: labels starting with
`Unit`, `E2E`, or `Component integration` / `Contract` / `UI integration`
(including `Contract and component integration`) map to those tiers; anything
else is tier-unassigned. `manual` and `not automatable` cases are excluded with
a reason.

Metric states:

- `value` - a measured percentage, for example `69.8% (405/580)`.
- `zero` - measured zero; a real result.
- `unavailable` - no coverprofile was supplied.
- `blocked` - a mapped unit package is absent from the profile, or any
  automated behavior has no tier (shows `N tier-unassigned`).
- `na` - no test plan for that tier, or no statements in the mapped packages
  (blocks the verdict).
- `none` - a plan exists but has no automated behaviors at that tier ("no cases
  at this tier"); neutral, and it does not block the verdict.

Unit shows statement-weighted coverage plus the legacy package average.
Integration and E2E show covered / automated behaviors at that tier. Unit-tier
behaviors are listed but not scored: unit coverage comes from the profile.

## External test plans

Two areas in `features.yaml` read their test plans from the
`enhancement-proposals` repository: `iam` (OSAC-2476) and `secrets`
(OSAC-5343). That repository is a gitignored checkout that
`tools/bootstrap.sh` creates; a fresh clone and the nightly report job
run without it.

A missing plan file is not an error. Its behaviors are simply not loaded,
so a tier with no other behaviors shows `na` with the evidence
`no test plan`, and the verdict reason reads for example
`Integration: no test plan`. There, `na` means the plan is not available
in the checkout, not that coverage is missing. Only the plan-backed tiers
go `na`: an E2E row such as `0.0% (0/8)` is real evidence from the
inline `behaviors:` and `mappings:` in `features.yaml`, and the missing
plan does not explain it. With the checkout present, the same area can
show `blocked` or gaps that come from the plan's real content.

To make the plans readable, run `tools/bootstrap.sh`, which clones
`enhancement-proposals/` into the repository root. A symlink named
`enhancement-proposals` at the repository root pointing at an existing
checkout also works; the reporter trusts it automatically and lists it
under `External roots` in the report. `--allow-external-root` trusts
other external roots, but the plan must still exist at its
repository-root-relative path: a missing file is detected before the
flag is consulted, so it cannot stand in for the checkout itself.

## Verdict

- `not ready` - any critical/high gap.
- `insufficient evidence` - otherwise, any metric is `unavailable`, `blocked`,
  `na`, or `zero` (a `none` tier is neutral). A `zero` tier says whether it
  reflects drafted links pending review or no reviewed test links yet.
- `ready` - otherwise.

Each area and overview row also carries `verdict_reason`, the strongest cause
in one sentence.

## Report contents

The Markdown report has the header (feature, commit), the verdict, the tier
table with states and reasons, the unit package table, inferred mappings,
gaps sorted critical/high/medium/low with a next action, and a five-line
legend. Pipes and newlines in table cells are escaped.

## Tests

```sh
python3 -m unittest discover -s tools/coverage-report/tests
```

CI runs the same command from `.github/workflows/coverage-report-tests.yml`.

## Not in v1

The previous prototype grew to about 21,000 lines. This rewrite deliberately
drops everything that was not needed for the Feature x tier answer:

- Ginkgo, JUnit XML, and per-spec execution ingestion (only coverprofiles are
  measured).
- Coverage-note, filename-keyword, and TC-reference heuristics in test code.
- Tier overrides, boundary scoring, and requirement/interface-change coverage.
- JSON schemas, publish/sidecar flows, diffs, trends, and dashboards.
- UI/TypeScript coverage and suite-root inference for bare test names.
- Per-path gaps: a behavior with several planned paths gets one gap naming the
  most specific path.
- Partial-overlap deduplication across profiles: only identical blocks merge.
- A review workflow; `mappings_reviewed_by` is trusted as written.
