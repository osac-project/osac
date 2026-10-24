# Hybrid coverage report

`tools/coverage-report` computes per-feature, per-tier test coverage evidence
for OSAC and publishes a reproducible report:

- **Unit** from Go coverprofiles, weighted by statements.
- **Integration** and **E2E** from the feature test plan: every `TC-*` case is
  a behavior, and a behavior counts only when a human-reviewed mapping points
  at a test at the required tier.

The tool never guesses. Evidence it cannot measure is `unavailable` or `na`;
evidence it can measure badly is `zero`; an incomplete denominator is
`blocked` with a provisional percentage. It reads only files in the checked-out
repository: no network and no Jira calls.

## Quick start

```bash
# One feature (Jira key, registry name, or alias).
make coverage-report FEATURE=OSAC-2540

# Pin generated_at for a byte-identical rerun.
make coverage-report FEATURE=OSAC-2540 ARGS="--now 2026-10-22T00:00:00Z"

# Every feature in the registry (writes the ALL directory).
make coverage-report
```

The Makefile target is a thin wrapper around the script, which can also be run
directly:

```bash
tools/coverage-report/coverage-report OSAC-2540 --repo .
```

Outputs land under `.artifacts/coverage-report/<FEATURE>/` (or `ALL/`). The
command prints the four main paths.

## Inputs

### `.coverage-skill.yaml`

Optional at the repository root. Precedence is CLI flags > this file > built-in
defaults. Every effective value and the layer that supplied it are recorded in
`run-manifest.json`.

| Key | Meaning | Default |
|---|---|---|
| `version` | Configuration schema version (must be `1`). | `1` |
| `features_file` | Feature registry path. | bundled `tools/coverage-report/config/features.yaml` |
| `enhancement_roots` | Directories searched for test plans. | `enhancement-proposals/enhancements`, `.artifacts/design` |
| `artifacts.coverprofiles` | Glob patterns for Go coverprofiles. | `[]` |
| `artifacts.junit` | Glob patterns for JUnit XML reports. | `[]` |
| `metric_versions.unit` | `legacy-package-average-v1` or `statement-weighted-v2`. | `statement-weighted-v2` |
| `metric_versions.integration` / `.e2e` | `behavior-inventory-v1`. | `behavior-inventory-v1` |
| `policy.require_execution` | Count a declared mapping only when a current passing execution is joined. | `false` (pending QE sign-off) |
| `policy.unit_low_coverage_percent` | Statement percentage below which a measured unit package becomes a gap. | `50` (pending QE sign-off) |
| `output_dir` | Report root. | `.artifacts/coverage-report` |

### `features.yaml`

The version-controlled registry. One entry per feature:

```yaml
version: 1
features:
  - key: OSAC-2540
    name: disk-image
    aliases: [diskimage]
    testplan: enhancement-proposals/enhancements/OSAC-2540-disk-image/testplan.md
    components: [fulfillment-service]
    unit_packages:
      - package: github.com/osac-project/osac/fulfillment-service/internal/servers
        rationale: public and private DiskImage gRPC servers
    suites:
      integration: [fulfillment-service/it]
      e2e: [tests/e2e]
```

`unit_packages` accepts a package string or `{package, rationale}`. A trailing
`/...` selects a subtree. `suites` scopes the files searched for test
references and keyword heuristics; it does not by itself produce evidence.

### Test plans

A test plan is a Markdown file with `#### TC-...: title` headings under
`## Test Cases`. The registry's `testplan` path wins; otherwise the configured
`enhancement_roots` are searched for `<KEY>-*/testplan.md` and
`<KEY>/04-testplan.md`. The metadata table (or `- **Name:** value` bullets)
supplies:

- `Tier` / `Test Tier` / `Level` / `Test Type`: `unit`, `integration`, or `e2e`.
  This is **required** for a behavior to count. A case with no tier is
  *tier-unassigned*: it is excluded from both denominators, emits one gap, and
  makes the tier metrics **blocked** with a provisional percentage.
  The label vocabulary is wider than the three tiers: "Component integration",
  "Contract", "Contract and component integration", and "UI integration" map to
  integration. A mixed label such as
  "Unit / [DEV] plus component integration for the RPC" uses its **first** tier
  (unit) and records the rest as `tier_note`; the full text stays in
  `tier_label`.
- `Boundary`: `real` and/or `simulated`, for example
  `- **Boundary:** real (deployed API and database)`. When a behavior requires
  `real`, a mapping with only a simulated boundary does not cover it.
- `Automation`: a manual or out-of-scope value makes the behavior unassessable
  (counted separately, never a gap).
- `Priority`: drives gap ordering. `Story`/`AC`/`Interface Change` are parsed
  for context; the requirement anchor comes from the `### FR-...:` heading.

### `config/mappings/<KEY>.yaml`

The reviewed link between test-plan cases and tests:

```yaml
version: 1
feature: OSAC-2540
reviewed_by: "@qe-team"            # PENDING-REVIEW makes every entry inferred
behavior_tiers:                 # optional override, wins over the test plan
  TC-FR1-01: integration
mappings:
  - behavior: TC-FR1-01
    tests:
      - path: fulfillment-service/it/it_disk_images_api_test.go
        name: DiskImage API        # suite/classname or testcase name
        tier: integration
    boundary: {real: [fulfillment-api, postgresql], simulated: []}
    rationale: Create is exercised against the deployed API and database.
```

The repository file is `config/mappings/<KEY>.yaml` (relative to the repository
root); the bundled `tools/coverage-report/config/mappings/<KEY>.yaml` is the
fallback.

- `reviewed_by` must be a GitHub user handle (`@name`) or a team (`org/team`
  or `@org/team`); a bare user name is not accepted. Placeholders (`pending`,
  `todo`, `tbd`, `unreviewed`, `none`, `n/a`) are compared case- and
  separator-insensitively, so `PENDING-REVIEW` and `PENDINGREVIEW` both work;
  `true`/`false`/`yes`/`no` are never reviewers either. Any of these makes the
  file's entries `inferred_mapping`; they are reported but **never** count.
- `behavior_tiers` applies even in a `PENDING-REVIEW` file; the behavior then
  records `tier_reviewed: false` and the report counts it as an unreviewed tier
  assignment.
- A mapping whose `tests[].path` does not exist becomes a `gap_unknown`
  (`test-missing` gap), not coverage.
- `boundary.real` lists real dependencies; `boundary.simulated` lists fakes,
  mocks, envtest, or stub providers.

An approved test plan is also a mapping source. In a plan under
`enhancement-proposals/` (merged and therefore reviewed), a test path named in
a case's `Execution` bullet or in the Planning Evidence Matrix is a
**declared** mapping when it is not marked "proposed" and exists in the
checkout. A proposed or missing path produces a `planned-not-implemented` gap
with the action `Implement <path> (planned in testplan)`. A plan under
`.artifacts/design/` is an unmerged draft: its paths are inferred at most.
Commands that name no path are kept in the behavior detail and never treated
as tests.

### Command-line overrides and artifacts

| Flag | Meaning |
|---|---|
| `--repo PATH` | Repository root to report on. |
| `--sha SHA` | Commit to report under (default: `git rev-parse HEAD`). |
| `--config PATH` | Alternate `.coverage-skill.yaml`. |
| `--out PATH` | Alternate output directory. |
| `--coverprofile PATH` | Explicit Go coverprofile; repeatable. Overrides configured globs. |
| `--junit PATH` | Explicit JUnit XML; repeatable. Overrides configured globs. |
| `--previous PATH` | Compare against an earlier `report.json`. |
| `--now ISO8601` | Pin `generated_at` for byte-identical output. |

Without `--previous`, the report already published for the same scope is used
as the baseline, unless it has the same `run_id` (an identical rerun has
nothing to compare).

Artifacts carry no revision themselves, so provenance comes from a sidecar
named `<artifact>.meta.json`:

```json
{"revision": "0123456789abcdef0123456789abcdef01234567"}
```

- Coverprofile sidecars may also carry `command` and `generated_at`.
- JUnit sidecars may carry `revision`, `generated_at`, `ci_run_id`, `ci_url`.
  A `revision`, `git_sha`, or `commit` `<property>` on the `<testsuite>` is the
  fallback.
- Exact revision match or an unambiguous prefix of at least seven hexadecimal
  characters is `current`; anything else is `stale`; a missing revision is
  `unknown`.

Producing a unit coverprofile:

```bash
cd fulfillment-service
go test ./internal/... -coverprofile=../coverage.out
printf '{"revision": "%s"}\n' "$(git rev-parse HEAD)" > ../coverage.out.meta.json
```

Ginkgo works the same way (`ginkgo run -r --cover --coverprofile=coverage.out`).
For JUnit, `ginkgo run --junit-report=../it-report.integration.xml it`; the
`.integration.xml` suffix (or an `it/` path) supplies the tier.

## Outputs

| Path | Contents |
|---|---|
| `report.json` | Schema-validated report: summary, features, metrics, behaviors, evidence, inventories, gaps, diagnostics, comparison. |
| `report.md` | Readiness summary, coverage table, QE detail, change log, diagnostics. |
| `run-manifest.json` | Audit record: revision, effective configuration, config origins, input digests, collector status, inventories, command line. |
| `diagnostics/diagnostics.json` | Every diagnostic with code, severity, and recovery hint. |
| `inputs/digests.json` | SHA-256 and size for every input. |
| `inputs/<kind>/<file>` | Copies of explicitly passed `--coverprofile`/`--junit` files. |

`generated_at` is the only volatile field. JSON is canonical (sorted keys, fixed
indentation, trailing newline), so two runs with the same inputs and `--now`
produce byte-identical files.

Exit codes: `0` success, `1` internal/repository error, `2` scope error
(unknown or ambiguous feature), `3` configuration error.

## Evidence classes

Strongest first. Evidence never silently gains strength; the only allowed
transition is inferred -> declared (a human reviewed it).

| Class | Meaning | Counts? |
|---|---|---|
| `measured` | A collector measured the artifact (unit statements). | Yes, for unit. |
| `observed_execution` | A test ran; JUnit records pass/fail/skip and freshness. | Never establishes mapping. |
| `declared_mapping` | A reviewed mapping says this test covers the behavior. | Yes, at its tier. |
| `inferred_mapping` | Heuristic or `PENDING-REVIEW` suggestion, with confidence and heuristic name. | No. |
| `gap_unknown` | A mapping names a test that cannot be found. | No; produces a gap. |

## Metrics

### Unit

`statement-weighted-v2` (default):

```
sum(covered statements) / sum(total statements) * 100
```

`legacy-package-average-v1` (comparison only):

```
sum(per-package statement percent) / package count
```

It stores the sum of percentages in tenths as `numerator` and ten times the
package count as `denominator`, so `numerator / denominator` is the mean exactly
(for example `500/20` is `25.0%`). The report shows the configured version as
the metric and the other version in parentheses in the coverage table.

### Integration and E2E: `behavior-inventory-v1`

```
numerator   = assessable behaviors covered at this tier
denominator = assessable behaviors whose required tier is this tier
```

A behavior is covered when it has **at least one `declared_mapping` at that
tier** and, if it requires a boundary kind, the mapping's boundary provides it.
A higher tier does not satisfy a lower one, a lower tier does not satisfy a
higher one, and `inferred_mapping` never counts.

- Behaviors that require the other tier are counted in `excluded`.
- Non-assessable behaviors (manual, out of scope) are counted in
  `unassessable`.
- Behaviors with no tier are counted in `tier_unassigned`, are excluded from
  both denominators, and block both tier metrics.
- Unit-tier behaviors are **not** part of this metric. They are counted and
  listed in the unit detail for information only, because the unit tier is
  measured by package statement coverage.
- `inputs` lists the ids of the mapping records that contribute to the
  numerator.

### Execution join

JUnit `observed_execution` records are joined to covering mappings by testcase
identity (`<suite>::<name>`, Ginkgo's `[It] ` prefix removed) or by mapped
file path when the mapping has no name. Each behavior reports
`executed-pass-current`, `executed-fail`, `stale`, `not-executed`, or `unknown`.

With `policy.require_execution: false` (the v1 default) execution is reported
but does not gate the numerator. With `true`, only a current passing execution
counts.

## Metric states

| State | Meaning |
|---|---|
| `value` | A percentage over a complete denominator. |
| `zero` | A measured zero over a complete denominator. Not the same as missing evidence. |
| `na` | No inventory, or no assessable behavior requires this tier. |
| `unavailable` | No collector produced evidence for the tier. |
| `stale` | All evidence was produced at another revision. |
| `blocked` | The denominator is incomplete or conflicting: duplicate test-case ids, a mapped unit package missing from the profile, partially stale unit evidence, or **tier-unassigned behaviors**. |

A blocked behavior metric keeps its arithmetic over the tiered behaviors and
adds `provisional_percent` (the percentage it would report). `value_percent`
stays empty. When every behavior is tier-unassigned, the denominator is `0` and
`provisional_percent` is empty.

Tier-unassigned behaviors produce exactly **one** gap each
(`reason: tier-unassigned`, `tier: null`) with the action:

```text
Add a tier for <TC> in the testplan execution matrix or in
config/mappings/<KEY>.yaml behavior_tiers
```

Any tier-unassigned behavior makes the feature verdict `insufficient evidence`.

## Policies

Both policy values are defaults pending QE sign-off; change them in
`.coverage-skill.yaml` and record the decision in the PR or Jira ticket.

- **`policy.require_execution: false`** — mapping coverage and execution are
  reported separately. Set `true` to require a current passing run in the
  numerator.
- **`policy.unit_low_coverage_percent: 50`** — a measured unit package below
  this statement percentage becomes a `low-statement-coverage` gap. A package
  at exactly the threshold is not a gap.

## Adding a feature

1. Add an entry to `config/features.yaml` with its `testplan`, `unit_packages`,
   and `suites`.
2. Ensure every test case declares a `Tier` (and `Boundary` where it matters).
3. Add `config/mappings/<KEY>.yaml` with reviewed test mappings:
   `reviewed_by` is a person or team, not `PENDING-REVIEW`.
4. Run the report and work the gaps. `planned-not-implemented` means the plan
   names a test that does not exist yet; `inferred-only` means review the
   suggested mapping; `wrong-tier` means add a test at the required tier;
   `boundary-simulated` means the test does not exercise the required real
   boundary.
5. Only promote a mapping after checking that the named test actually asserts
   the behavior.

## Limitations

- **Shared packages skew unit percentages.** A package such as
  `fulfillment-service/internal/servers` serves many resources, so its
  statement percentage understates any one feature's coverage. The per-package
  figures are in the detail section; a feature-specific package is more
  meaningful.
- **TC ids are not globally unique.** Behavior ids are namespaced
  (`<KEY>/TC-...`), and mappings and `behavior_tiers` are per feature. Do not
  reuse a TC id inside one test plan; duplicates block the metrics.
- **JUnit attribution is not automatic.** A testcase is attributed to a feature
  when its identity names the feature key, name, or alias, or when it matches a
  mapping entry. Otherwise it is dropped with `EXECUTION_UNATTRIBUTED`.
- **Tier inference needs a hint.** JUnit testcases need an `it/`/`tests/e2e/`
  path, an `internal/` classname, or a `.unit.xml`/`.integration.xml`/
  `.e2e.xml` filename; otherwise they are skipped with `EXECUTION_TIER_UNKNOWN`.
- **Single-repository scope.** The tool reports on one checkout at a time and
  performs no network or Jira access. E2E artifacts must be brought into the
  checkout (or passed with `--junit`).
- **Keyword and test-reference heuristics are suggestions.** They can produce
  `inferred_mapping` evidence, which never counts and should be replaced by a
  reviewed entry.

## Example report excerpt

```markdown
## Readiness summary

| Feature | Verdict | Critical gaps | High gaps | Unavailable / stale / blocked | Zero metrics | Tier unassigned |
|---|---|---|---|---|---|---|
| OSAC-1111 | insufficient evidence | 1 | 3 | none | none | 1 |

- 1 behavior has no tier; Integration/E2E percentages are provisional.

## Coverage table

| Feature | Unit | Integration | E2E |
|---|---|---|---|
| OSAC-1111 (disk-image) | 33.3% (2/6) [statement-weighted-v2] (legacy-package-average-v1: 25.0%) | blocked (provisional 50.0% 2/4, 1 tier-unassigned) | blocked (provisional 100.0% 1/1, 1 tier-unassigned) |
```

## Tests

```bash
cd tools/coverage-report
python3 -m unittest discover -s tests
```

The suite uses only the standard library and `PyYAML`; it creates throwaway git
repositories, so it needs `git` but no cluster, network, or Jira access. It is
run on pull requests that touch the tool by
`.github/workflows/coverage-report-tests.yml`.
