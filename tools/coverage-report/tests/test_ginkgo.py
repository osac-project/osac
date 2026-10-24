"""Contract tests for the Ginkgo/Go unit-coverage collector."""

import json
import random
import tempfile
import unittest
from pathlib import Path

import path_setup  # noqa: F401  (adds the package root to sys.path)

import support
from coverage_report.collectors import CollectorContext
from coverage_report.collectors.ginkgo import (
    COLLECTOR_NAME,
    COVERAGE_KIND,
    MISSING_KIND,
    GinkgoUnitCollector,
    _Block,
    _merge_blocks,
    parse_coverprofile,
)
from coverage_report.config import EffectiveConfig
from coverage_report.model import EvidenceClass, Freshness, MetricState, Tier
from coverage_report.unit_metrics import unit_scope_revision
from coverage_report.scope import FeatureEntry, UnitPackage, load_registry

SHA = "a" * 40
OTHER_SHA = "b" * 40
RUN_ID = "run-test"


def make_config(
    *,
    coverprofiles=(),
    unit_version="statement-weighted-v2",
) -> EffectiveConfig:
    return EffectiveConfig(
        version=1,
        features_file="",
        enhancement_roots=(),
        coverprofiles=tuple(coverprofiles),
        junit=(),
        metric_versions={
            "unit": unit_version,
            "integration": "behavior-inventory-v1",
            "e2e": "behavior-inventory-v1",
        },
        output_dir=".artifacts/coverage-report",
        origins={},
    )


def make_feature(*packages: str, key: str = "OSAC-1000") -> FeatureEntry:
    return FeatureEntry(
        key=key,
        name=key.lower(),
        unit_packages=tuple(
            UnitPackage(package=package, rationale="test mapping")
            for package in packages
        ),
    )


def make_ctx(
    repo_root,
    features,
    *,
    explicit=(),
    globs=(),
    unit_version="statement-weighted-v2",
    sha=SHA,
) -> CollectorContext:
    return CollectorContext(
        repo_root=str(repo_root),
        sha=sha,
        features=tuple(features),
        config=make_config(coverprofiles=globs, unit_version=unit_version),
        run_id=RUN_ID,
        explicit_inputs={"coverprofile": tuple(str(path) for path in explicit)},
    )


def write_profile(root, name="cover.out", text=None) -> str:
    path = Path(root, name)
    path.write_text(
        text
        if text is not None
        else (
            "mode: count\n"
            "example.com/mod/pkg/alpha/a.go:1.1,2.2 2 1\n"
            "example.com/mod/pkg/alpha/a.go:3.1,4.2 3 0\n"
        ),
        encoding="utf-8",
    )
    return str(path)


def collect(ctx):
    return GinkgoUnitCollector().collect(ctx)


def diagnostics_by_code(batch, code):
    return [item for item in batch.diagnostics if item.code == code]


class TestParser(unittest.TestCase):
    def test_each_mode_is_accepted(self):
        for mode in ("set", "count", "atomic"):
            text = "mode: {0}\npkg/a.go:1.1,2.2 1 1\n".format(mode)
            parsed_mode, packages, issues = parse_coverprofile(
                text, source="cover.out"
            )
            self.assertEqual(parsed_mode, mode)
            self.assertEqual(issues, ())
            self.assertEqual(packages["pkg"].statements, 1)

    def test_malformed_line_keeps_valid_lines_and_names_the_line(self):
        text = (
            "mode: set\n"
            "pkg/a.go:1.1,2.2 1 1\n"
            "this is not a block\n"
            "pkg/a.go:3.1,4.2 2 0\n"
        )
        mode, packages, issues = parse_coverprofile(text, source="cover.out")
        self.assertEqual(mode, "set")
        self.assertEqual(packages["pkg"].statements, 3)
        self.assertEqual(packages["pkg"].covered, 1)
        self.assertEqual(len(issues), 1)
        self.assertEqual(issues[0].code, "UNIT_COVERPROFILE_LINE_MALFORMED")
        self.assertIn("cover.out:3", issues[0].message)

    def test_truncated_final_line_is_malformed_not_fatal(self):
        text = "mode: set\npkg/a.go:1.1,2.2 1 1\npkg/a.go:3.1,4.2 2"
        _, packages, issues = parse_coverprofile(text, source="cover.out")
        self.assertEqual(packages["pkg"].statements, 1)
        self.assertEqual(
            [issue.code for issue in issues], ["UNIT_COVERPROFILE_LINE_MALFORMED"]
        )

    def test_empty_input_produces_no_packages_and_a_diagnostic(self):
        mode, packages, issues = parse_coverprofile("", source="cover.out")
        self.assertIsNone(mode)
        self.assertEqual(packages, {})
        self.assertEqual([issue.code for issue in issues], ["UNIT_COVERPROFILE_EMPTY"])

    def test_header_only_profile_is_reported_as_empty(self):
        _, packages, issues = parse_coverprofile(
            "mode: set\n", source="cover.out"
        )
        self.assertEqual(packages, {})
        self.assertEqual([issue.code for issue in issues], ["UNIT_COVERPROFILE_EMPTY"])

    def test_missing_header_is_tolerated(self):
        mode, packages, issues = parse_coverprofile(
            "pkg/a.go:1.1,2.2 1 1\n", source="cover.out"
        )
        self.assertIsNone(mode)
        self.assertEqual(packages["pkg"].statements, 1)
        self.assertEqual(
            [issue.code for issue in issues], ["UNIT_COVERPROFILE_MODE_MISSING"]
        )

    def test_unknown_mode_is_reported_but_parsed(self):
        mode, packages, issues = parse_coverprofile(
            "mode: weird\npkg/a.go:1.1,2.2 1 1\n", source="cover.out"
        )
        self.assertEqual(mode, "weird")
        self.assertEqual(packages["pkg"].statements, 1)
        self.assertEqual(
            [issue.code for issue in issues], ["UNIT_COVERPROFILE_MODE_UNKNOWN"]
        )

    def test_duplicate_blocks_take_the_maximum_count(self):
        text = (
            "mode: count\n"
            "pkg/a.go:1.1,2.2 2 0\n"
            "pkg/a.go:1.1,2.2 2 1\n"
            "pkg/a.go:1.1,2.2 2 0\n"
        )
        _, packages, issues = parse_coverprofile(text, source="cover.out")
        self.assertEqual(issues, ())
        self.assertEqual(packages["pkg"].blocks, 1)
        self.assertEqual(packages["pkg"].statements, 2)
        self.assertEqual(packages["pkg"].covered, 2)

    def test_duplicate_blocks_take_the_maximum_statement_count(self):
        text = (
            "mode: count\n"
            "pkg/a.go:1.1,2.2 1 1\n"
            "pkg/a.go:1.1,2.2 3 1\n"
        )
        _, packages, _ = parse_coverprofile(text, source="cover.out")
        self.assertEqual(packages["pkg"].statements, 3)

    def test_line_order_does_not_change_the_result(self):
        lines = [
            "pkg/a.go:1.1,2.2 2 1",
            "pkg/a.go:3.1,4.2 3 0",
            "pkg/a.go:1.1,2.2 2 0",
            "pkg/b.go:1.1,2.2 1 1",
        ]
        expected = parse_coverprofile(
            "mode: count\n" + "\n".join(lines) + "\n", source="cover.out"
        )[1]
        for seed in range(5):
            shuffled = list(lines)
            random.Random(seed).shuffle(shuffled)
            _, packages, _ = parse_coverprofile(
                "mode: count\n" + "\n".join(shuffled) + "\n", source="cover.out"
            )
            self.assertEqual(packages, expected)

    def test_block_without_package_path_is_skipped(self):
        _, packages, issues = parse_coverprofile(
            "mode: set\norphan.go:1.1,2.2 1 1\n", source="cover.out"
        )
        self.assertEqual(packages, {})
        self.assertEqual(
            [issue.code for issue in issues],
            ["UNIT_COVERPROFILE_PACKAGE_UNKNOWN", "UNIT_COVERPROFILE_EMPTY"],
        )


class TestDiscovery(unittest.TestCase):
    def test_explicit_input_wins_over_configured_globs(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            explicit = write_profile(
                tmpdir,
                "explicit.out",
                "mode: set\nexample.com/mod/pkg/alpha/a.go:1.1,2.2 2 1\n",
            )
            globbed = write_profile(
                tmpdir,
                "artifacts.out",
                "mode: set\nexample.com/mod/pkg/beta/b.go:1.1,2.2 2 1\n",
            )
            ctx = make_ctx(
                tmpdir,
                [make_feature("example.com/mod/pkg/alpha")],
                explicit=[explicit],
                globs=(globbed,),
            )
            batch = collect(ctx)

        measured = [r for r in batch.records if r.kind == COVERAGE_KIND]
        self.assertEqual([record.subject for record in measured], ["example.com/mod/pkg/alpha"])

    def test_configured_globs_are_sorted(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            write_profile(
                tmpdir,
                "b.out",
                "mode: set\nexample.com/mod/pkg/alpha/a.go:3.1,4.2 1 0\n",
            )
            write_profile(
                tmpdir,
                "a.out",
                "mode: set\nexample.com/mod/pkg/alpha/a.go:1.1,2.2 1 1\n",
            )
            ctx = make_ctx(
                tmpdir,
                [make_feature("example.com/mod/pkg/alpha")],
                globs=(str(Path(tmpdir, "*.out")),),
            )
            batch = collect(ctx)

        measured = [r for r in batch.records if r.kind == COVERAGE_KIND]
        self.assertEqual(
            [record.source for record in measured], ["a.out,b.out"]
        )
        self.assertEqual(measured[0].details["statements"], 2)
        self.assertEqual(measured[0].details["covered"], 1)

    def test_unreadable_explicit_input_is_reported_not_fatal(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            missing = str(Path(tmpdir, "does-not-exist.out"))
            ctx = make_ctx(
                tmpdir,
                [make_feature("example.com/mod/pkg/alpha")],
                explicit=[missing],
            )
            batch = collect(ctx)

        self.assertEqual(batch.records, ())
        self.assertEqual(batch.metrics[0].state, MetricState.UNAVAILABLE)
        unreadable = diagnostics_by_code(batch, "UNIT_COVERPROFILE_UNREADABLE")
        self.assertEqual(len(unreadable), 1)
        self.assertEqual(unreadable[0].severity, "error")

    def test_no_input_emits_no_records_and_never_zero(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            ctx = make_ctx(
                tmpdir, [make_feature("example.com/mod/pkg/alpha")]
            )
            batch = collect(ctx)

        self.assertEqual(batch.records, ())
        metric = batch.metrics[0]
        self.assertEqual(metric.state, MetricState.UNAVAILABLE)
        self.assertIsNone(metric.value_percent)
        self.assertEqual(
            len(diagnostics_by_code(batch, "UNIT_EVIDENCE_UNAVAILABLE")), 1
        )

    def test_no_input_without_mapped_packages_is_blocked_for_scope(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            ctx = make_ctx(tmpdir, [make_feature()])
            batch = collect(ctx)

        self.assertEqual(batch.metrics[0].state, MetricState.BLOCKED)
        self.assertIsNone(batch.metrics[0].value_percent)


class TestUnitScopeRevision(unittest.TestCase):
    def test_metric_revision_is_the_scope_digest_not_the_commit(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            profile = write_profile(tmpdir)
            ctx = make_ctx(
                tmpdir,
                [make_feature("example.com/mod/pkg/alpha")],
                explicit=[profile],
                sha="f" * 40,
            )
            batch = collect(ctx)

        metric = batch.metrics[0]
        self.assertEqual(
            metric.inventory_revision,
            unit_scope_revision(["example.com/mod/pkg/alpha"]),
        )
        self.assertNotEqual(metric.inventory_revision, "f" * 40)


class TestProvenance(unittest.TestCase):
    def _run(self, revision, sha=SHA, unit_version="statement-weighted-v2"):
        with tempfile.TemporaryDirectory() as tmpdir:
            profile = write_profile(tmpdir)
            if revision is not None:
                Path(profile + ".meta.json").write_text(
                    json.dumps({"revision": revision}), encoding="utf-8"
                )
            ctx = make_ctx(
                tmpdir,
                [make_feature("example.com/mod/pkg/alpha")],
                explicit=[profile],
                sha=sha,
                unit_version=unit_version,
            )
            return collect(ctx)

    def test_matching_revision_is_current(self):
        batch = self._run(SHA)
        self.assertEqual(
            {record.freshness for record in batch.records}, {Freshness.CURRENT}
        )
        self.assertEqual(
            {record.revision for record in batch.records}, {SHA}
        )
        self.assertEqual(diagnostics_by_code(batch, "UNIT_REVISION_UNKNOWN"), [])

    def test_abbreviated_revision_prefix_is_current(self):
        batch = self._run(SHA[:12])
        self.assertEqual(
            {record.freshness for record in batch.records}, {Freshness.CURRENT}
        )

    def test_too_short_a_prefix_is_stale(self):
        # Seven characters is the shared minimum; a six-character prefix is
        # ambiguous and must not be trusted as the reported commit.
        batch = self._run(SHA[:6])
        self.assertEqual(
            {record.freshness for record in batch.records}, {Freshness.STALE}
        )

    def test_different_revision_is_stale(self):
        batch = self._run(OTHER_SHA)
        self.assertEqual(
            {record.freshness for record in batch.records}, {Freshness.STALE}
        )
        self.assertEqual(batch.metrics[0].state, MetricState.STALE)
        self.assertIsNone(batch.metrics[0].value_percent)

    def test_absent_revision_is_unknown_with_diagnostic(self):
        batch = self._run(None)
        self.assertEqual(
            {record.freshness for record in batch.records}, {Freshness.UNKNOWN}
        )
        unknown = diagnostics_by_code(batch, "UNIT_REVISION_UNKNOWN")
        self.assertEqual(len(unknown), 1)
        self.assertEqual(unknown[0].severity, "warning")
        # Unknown freshness does not block a measured value.
        self.assertEqual(batch.metrics[0].state, MetricState.VALUE)

    def test_malformed_sidecar_is_reported_and_treated_as_absent(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            profile = write_profile(tmpdir)
            Path(profile + ".meta.json").write_text("{not json", encoding="utf-8")
            ctx = make_ctx(
                tmpdir,
                [make_feature("example.com/mod/pkg/alpha")],
                explicit=[profile],
            )
            batch = collect(ctx)

        self.assertEqual(
            {record.freshness for record in batch.records}, {Freshness.UNKNOWN}
        )
        malformed = diagnostics_by_code(batch, "UNIT_META_MALFORMED")
        self.assertEqual(len(malformed), 1)
        self.assertEqual(malformed[0].severity, "warning")


class TestMapping(unittest.TestCase):
    def test_measured_record_carries_package_details(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            profile = write_profile(tmpdir)
            ctx = make_ctx(
                tmpdir,
                [make_feature("example.com/mod/pkg/alpha")],
                explicit=[profile],
            )
            batch = collect(ctx)

        record = batch.records[0]
        self.assertEqual(record.evidence_class, EvidenceClass.MEASURED)
        self.assertEqual(record.tier, Tier.UNIT)
        self.assertEqual(record.collector, COLLECTOR_NAME)
        self.assertEqual(record.kind, COVERAGE_KIND)
        self.assertEqual(record.subject, "example.com/mod/pkg/alpha")
        self.assertEqual(
            record.details["statements"], 5
        )
        self.assertEqual(record.details["covered"], 2)
        self.assertEqual(record.details["blocks"], 2)
        self.assertEqual(record.details["percent"], 40.0)

    def test_missing_package_is_gap_unknown_not_zero(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            profile = write_profile(tmpdir)
            ctx = make_ctx(
                tmpdir,
                [make_feature("example.com/mod/pkg/holiday")],
                explicit=[profile],
            )
            batch = collect(ctx)

        self.assertEqual(len(batch.records), 1)
        record = batch.records[0]
        self.assertEqual(record.evidence_class, EvidenceClass.GAP_UNKNOWN)
        self.assertEqual(record.kind, MISSING_KIND)
        self.assertEqual(record.subject, "example.com/mod/pkg/holiday")
        self.assertNotIn("percent", record.details)
        self.assertEqual(batch.metrics[0].state, MetricState.BLOCKED)
        self.assertIsNone(batch.metrics[0].value_percent)
        self.assertEqual(
            len(diagnostics_by_code(batch, "UNIT_PACKAGE_MISSING")), 1
        )

    def test_prefix_pattern_matches_subtree_packages(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            profile = write_profile(
                tmpdir,
                text=(
                    "mode: set\n"
                    "example.com/mod/pkg/alpha/a.go:1.1,2.2 1 1\n"
                    "example.com/mod/pkg/alpha/sub/b.go:1.1,2.2 1 0\n"
                    "example.com/mod/pkg/beta/b.go:1.1,2.2 1 1\n"
                ),
            )
            ctx = make_ctx(
                tmpdir,
                [make_feature("example.com/mod/pkg/alpha/...")],
                explicit=[profile],
            )
            batch = collect(ctx)

        self.assertEqual(
            [record.subject for record in batch.records],
            ["example.com/mod/pkg/alpha", "example.com/mod/pkg/alpha/sub"],
        )

    def test_exact_package_shared_by_two_features_is_allowed(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            profile = write_profile(tmpdir)
            ctx = make_ctx(
                tmpdir,
                [
                    make_feature(
                        "example.com/mod/pkg/alpha", key="OSAC-1000"
                    ),
                    make_feature(
                        "example.com/mod/pkg/alpha", key="OSAC-2000"
                    ),
                ],
                explicit=[profile],
            )
            batch = collect(ctx)

        self.assertEqual(
            diagnostics_by_code(batch, "UNIT_PACKAGE_AMBIGUOUS"), []
        )
        self.assertEqual(
            {record.feature for record in batch.records},
            {"OSAC-1000", "OSAC-2000"},
        )

    def test_overlapping_prefix_is_ambiguous(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            profile = write_profile(tmpdir)
            ctx = make_ctx(
                tmpdir,
                [
                    make_feature("example.com/mod/pkg/...", key="OSAC-1000"),
                    make_feature(
                        "example.com/mod/pkg/alpha", key="OSAC-2000"
                    ),
                ],
                explicit=[profile],
            )
            batch = collect(ctx)

        ambiguous = diagnostics_by_code(batch, "UNIT_PACKAGE_AMBIGUOUS")
        self.assertEqual(len(ambiguous), 1)
        self.assertEqual(ambiguous[0].severity, "error")
        self.assertIn("OSAC-1000", ambiguous[0].message)
        self.assertIn("OSAC-2000", ambiguous[0].message)

    def test_two_identical_prefixes_are_ambiguous(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            profile = write_profile(tmpdir)
            ctx = make_ctx(
                tmpdir,
                [
                    make_feature("example.com/mod/pkg/...", key="OSAC-1000"),
                    make_feature("example.com/mod/pkg/...", key="OSAC-2000"),
                ],
                explicit=[profile],
            )
            batch = collect(ctx)

        self.assertEqual(
            len(diagnostics_by_code(batch, "UNIT_PACKAGE_AMBIGUOUS")), 1
        )

    def test_collector_merges_two_profiles_without_double_counting(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            first = write_profile(
                tmpdir,
                "first.out",
                "mode: count\nexample.com/mod/pkg/alpha/a.go:1.1,2.2 2 0\n",
            )
            second = write_profile(
                tmpdir,
                "second.out",
                "mode: count\nexample.com/mod/pkg/alpha/a.go:1.1,2.2 2 4\n",
            )
            ctx = make_ctx(
                tmpdir,
                [make_feature("example.com/mod/pkg/alpha")],
                explicit=[first, second],
            )
            batch = collect(ctx)

        measured = [r for r in batch.records if r.kind == COVERAGE_KIND]
        self.assertEqual(len(measured), 1)
        self.assertEqual(measured[0].details["statements"], 2)
        self.assertEqual(measured[0].details["covered"], 2)
        # Explicit inputs are sorted before they are recorded as the source.
        self.assertEqual(measured[0].source, "first.out,second.out")

    def test_collection_is_deterministic_under_line_shuffle(self):
        lines = [
            "example.com/mod/pkg/alpha/a.go:1.1,2.2 2 1",
            "example.com/mod/pkg/alpha/a.go:3.1,4.2 3 0",
            "example.com/mod/pkg/alpha/a.go:1.1,2.2 2 0",
            "example.com/mod/pkg/beta/b.go:1.1,2.2 1 1",
        ]
        feature = make_feature(
            "example.com/mod/pkg/alpha", "example.com/mod/pkg/beta"
        )
        runs = []
        for seed in range(4):
            shuffled = list(lines)
            random.Random(seed).shuffle(shuffled)
            with tempfile.TemporaryDirectory() as tmpdir:
                profile = write_profile(
                    tmpdir, text="mode: count\n" + "\n".join(shuffled) + "\n"
                )
                ctx = make_ctx(tmpdir, [feature], explicit=[profile])
                batch = collect(ctx)
                runs.append(
                    (
                        [record.to_dict() for record in batch.records],
                        [metric.to_dict() for metric in batch.metrics],
                        [item.to_dict() for item in batch.diagnostics],
                    )
                )
        for run in runs[1:]:
            self.assertEqual(run, runs[0])


class TestCompatibilityFixture(unittest.TestCase):
    """Reproduce the hand-written fixture exactly.

    This is the regression guard for the formula compatibility contract: the
    legacy mean and the statement-weighted metric must keep producing the same
    numbers for the same profile.
    """

    @classmethod
    def setUpClass(cls):
        cls.fixture_dir = support.FIXTURES / "ginkgo"
        cls.expected = json.loads(
            (cls.fixture_dir / "expected.json").read_text(encoding="utf-8")
        )
        cls.registry = load_registry(str(cls.fixture_dir / "features.yaml"))

    def _collect(self, unit_version):
        with tempfile.TemporaryDirectory() as tmpdir:
            profile = Path(tmpdir, "cover.out")
            profile.write_text(
                (self.fixture_dir / "coverprofile.txt").read_text(encoding="utf-8"),
                encoding="utf-8",
            )
            ctx = make_ctx(
                tmpdir,
                self.registry.features,
                explicit=[profile],
                unit_version=unit_version,
            )
            return collect(ctx)

    def _assert_metric(self, batch, feature_key, version):
        expected = self.expected["features"][feature_key]
        expected_metric = expected[version]
        metric = next(
            item for item in batch.metrics if item.feature == feature_key
        )
        records = [r for r in batch.records if r.feature == feature_key]
        contributions = {
            record.subject: record.details.get("percent")
            for record in records
            if record.kind == COVERAGE_KIND
        }
        missing = sorted(
            record.subject for record in records if record.kind == MISSING_KIND
        )
        duplicates = sorted(
            "{0} (blocks={1}, statements={2})".format(
                record.subject,
                record.details.get("blocks"),
                record.details.get("statements"),
            )
            for record in records
            if record.kind == COVERAGE_KIND
        )
        message = (
            "\nfeature={feature} metric={version}"
            "\nexpected={expected}"
            "\nactual state={state} value_percent={value!r} "
            "numerator={numerator!r} denominator={denominator!r}"
            "\nformula={formula}"
            "\nper-package contributions={contributions}"
            "\nmissing packages={missing}"
            "\npackage block/statement inventory (duplicates collapsed)={dups}"
        ).format(
            feature=feature_key,
            version=version,
            expected=expected_metric,
            state=metric.state.value,
            value=metric.value_percent,
            numerator=metric.numerator,
            denominator=metric.denominator,
            formula=metric.formula,
            contributions=contributions,
            missing=missing,
            dups=duplicates,
        )

        self.assertEqual(metric.state.value, expected_metric["state"], message)
        self.assertEqual(
            metric.value_percent, expected_metric["value_percent"], message
        )
        self.assertEqual(
            metric.numerator, expected_metric["numerator"], message
        )
        self.assertEqual(
            metric.denominator, expected_metric["denominator"], message
        )

    def test_legacy_package_average_matches_expected(self):
        batch = self._collect("legacy-package-average-v1")
        for feature_key in self.expected["features"]:
            self._assert_metric(
                batch, feature_key, "legacy-package-average-v1"
            )

    def test_statement_weighted_matches_expected(self):
        batch = self._collect("statement-weighted-v2")
        for feature_key in self.expected["features"]:
            self._assert_metric(batch, feature_key, "statement-weighted-v2")

    def test_per_package_details_match_expected(self):
        batch = self._collect("statement-weighted-v2")
        for feature_key, expected in self.expected["features"].items():
            actual = {
                record.subject: {
                    "statements": record.details.get("statements"),
                    "covered": record.details.get("covered"),
                    "blocks": record.details.get("blocks"),
                    "percent": record.details.get("percent"),
                }
                for record in batch.records
                if record.feature == feature_key
                and record.kind == COVERAGE_KIND
            }
            self.assertEqual(actual, expected["packages"], feature_key)
            self.assertEqual(
                sorted(
                    record.subject
                    for record in batch.records
                    if record.feature == feature_key
                    and record.kind == MISSING_KIND
                ),
                expected["missing"],
                feature_key,
            )


class TestBlockMerge(unittest.TestCase):
    def test_merge_blocks_deduplicates_the_same_position_across_profiles(self):
        block = _Block(
            file="example.com/mod/pkg/alpha/a.go",
            start_line=1,
            start_col=1,
            end_line=2,
            end_col=2,
            statements=2,
            count=0,
        )
        from coverage_report.collectors.ginkgo import ProfileCoverage

        first = ProfileCoverage(
            path="a.out",
            mode="count",
            revision=None,
            command=None,
            generated_at=None,
            freshness=Freshness.UNKNOWN,
            packages={},
            blocks={block.key: block},
        )
        covered = _Block(
            file=block.file,
            start_line=1,
            start_col=1,
            end_line=2,
            end_col=2,
            statements=2,
            count=3,
        )
        second = ProfileCoverage(
            path="b.out",
            mode="count",
            revision=None,
            command=None,
            generated_at=None,
            freshness=Freshness.UNKNOWN,
            packages={},
            blocks={covered.key: covered},
        )

        merged = _merge_blocks([first, second])

        package = merged["example.com/mod/pkg/alpha"]
        self.assertEqual(package.coverage.blocks, 1)
        self.assertEqual(package.coverage.statements, 2)
        self.assertEqual(package.coverage.covered, 2)
        self.assertEqual(package.source, "a.out,b.out")


if __name__ == "__main__":
    unittest.main()
