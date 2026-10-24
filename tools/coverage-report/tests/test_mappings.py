"""Contract tests for the test-to-behavior mapping collector."""

import tempfile
import unittest
from pathlib import Path

import path_setup  # noqa: F401  (adds the package root to sys.path)
from coverage_report.collectors import CollectorContext
from coverage_report.collectors.mappings import (
    COLLECTOR_NAME,
    KIND_CONFIG,
    KIND_COVERAGE_NOTE,
    KIND_KEYWORD,
    KIND_MISSING_TEST,
    KIND_PLANNED_MISSING,
    KIND_TESTPLAN_EXECUTION,
    KIND_TEST_REFERENCE,
    TestMappingCollector,
    _expand_tc_ids,
)
from coverage_report.config import EffectiveConfig
from coverage_report.model import EvidenceClass, Tier
from coverage_report.scope import FeatureEntry

FIXTURES = Path(__file__).resolve().parent / "fixtures"


def fixture(*parts: str) -> str:
    return FIXTURES.joinpath(*parts).read_text(encoding="utf-8")


TESTPLAN = fixture("behaviors", "format-a.md")

PLAIN_API_TEST = """package it

var _ = Describe("DiskImage API", func() {
})
"""


def write_file(root: Path, relative: str, text: str) -> Path:
    path = Path(root, relative)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")
    return path


def make_feature(**overrides) -> FeatureEntry:
    values = {
        "key": "OSAC-1111",
        "name": "disk-image",
        "aliases": (),
        "testplan": "docs/testplan.md",
        "components": ("fulfillment-service",),
        "unit_packages": (),
        "suites": {
            "integration": ("fulfillment-service/it",),
            "e2e": ("tests/e2e",),
        },
    }
    values.update(overrides)
    return FeatureEntry(**values)


def make_context(root: Path, features) -> CollectorContext:
    config = EffectiveConfig(
        version=1,
        features_file=str(Path(root, "features.yaml")),
        enhancement_roots=(),
        coverprofiles=(),
        junit=(),
        metric_versions={
            "unit": "statement-weighted-v2",
            "integration": "behavior-inventory-v1",
            "e2e": "behavior-inventory-v1",
        },
        output_dir=str(Path(root, "out")),
        origins={},
        config_path=None,
    )
    return CollectorContext(
        repo_root=str(root),
        sha="0" * 40,
        features=tuple(features),
        config=config,
        run_id="run-test",
    )


def of_kind(batch, kind: str) -> list:
    return [record for record in batch.records if record.kind == kind]


def codes(batch) -> list:
    return [diagnostic.code for diagnostic in batch.diagnostics]


def diagnostic_with(batch, code):
    for diagnostic in batch.diagnostics:
        if diagnostic.code == code:
            return diagnostic
    raise AssertionError("no diagnostic with code {0} in {1}".format(code, codes(batch)))


class MappingFixture(unittest.TestCase):
    """Base class building a repo with a test plan and optional inputs."""

    def build(
        self,
        *,
        mapping_config: str = "",
        test_files=None,
        integration_doc: str = "",
        testplan: str = TESTPLAN,
        feature: FeatureEntry = None,
    ):
        tmpdir = tempfile.TemporaryDirectory()
        self.addCleanup(tmpdir.cleanup)
        root = Path(tmpdir.name)

        if testplan:
            write_file(root, "docs/testplan.md", testplan)
        if mapping_config:
            write_file(root, "config/mappings/OSAC-1111.yaml", mapping_config)
        if integration_doc:
            write_file(root, "docs/INTEGRATION-TESTING.md", integration_doc)
        for relative, text in (test_files or {}).items():
            write_file(root, relative, text)

        ctx = make_context(root, [feature or make_feature()])
        return root, ctx, TestMappingCollector().collect(ctx)


APPROVED_TESTPLAN = "enhancement-proposals/enhancements/OSAC-1111-mcp/testplan.md"
DRAFT_TESTPLAN = ".artifacts/design/OSAC-1111/04-testplan.md"
EXISTING_TEST = "fulfillment-service/it/exists_test.go"


def execution_plan(execution: str, *, boundary: str = "") -> str:
    boundary_line = (
        "\n- **Boundary:** {0}".format(boundary) if boundary else ""
    )
    return (
        "# Testplan — OSAC-1111\n\n"
        "## Test Cases\n\n"
        "### FR-1: One behavior\n\n"
        "#### TC-FR1-01: Create a thing\n\n"
        "| Tier | Priority | Automation |\n"
        "|---|---|---|\n"
        "| integration | critical | automated |\n\n"
        "##### Preconditions\n\n"
        "- **Tier / owner:** Component integration / [DEV].\n"
        "- **Execution:** {0}{1}\n".format(execution, boundary_line)
    )


def matrix_plan(path_cell: str) -> str:
    return (
        "# Testplan — OSAC-1111\n\n"
        "## Test Cases\n\n"
        "### FR-1: One behavior\n\n"
        "#### TC-FR1-01: Create a thing\n\n"
        "| Tier | Priority | Automation |\n"
        "|---|---|---|\n"
        "| integration | critical | automated |\n\n"
        "## Planning Evidence Matrix\n\n"
        "| Component / behavior | Test case IDs | Tier / owner | "
        "Existing or proposed path and command | Real dependencies | "
        "Simulated or omitted dependencies |\n"
        "|---|---|---|---|---|---|\n"
        "| MCP registry | TC-FR1-01 | Component integration | {0} | "
        "Deployed API, PostgreSQL | Provider omitted |\n".format(path_cell)
    )


class TestTestplanExecution(MappingFixture):
    def build_plan(self, plan: str, *, approved: bool = True, files=None):
        relative = APPROVED_TESTPLAN if approved else DRAFT_TESTPLAN
        test_files = dict(files or {})
        test_files[relative] = plan
        return self.build(
            testplan="",
            feature=make_feature(testplan=relative),
            test_files=test_files,
        )

    def test_existing_non_proposed_path_is_declared(self):
        _, _, batch = self.build_plan(
            execution_plan("`{0}`.".format(EXISTING_TEST)),
            files={EXISTING_TEST: "package it\n"},
        )

        records = of_kind(batch, KIND_TESTPLAN_EXECUTION)
        self.assertEqual(len(records), 1)
        record = records[0]
        self.assertEqual(record.evidence_class, EvidenceClass.DECLARED_MAPPING)
        self.assertEqual(record.subject, "OSAC-1111/TC-FR1-01")
        self.assertEqual(record.tier, Tier.INTEGRATION)
        self.assertEqual(record.details["path"], EXISTING_TEST)
        self.assertEqual(record.details["review_status"], "reviewed")
        self.assertFalse(record.details["proposed"])
        self.assertTrue(record.details["test_exists"])

    def test_proposed_path_is_a_planned_gap(self):
        _, _, batch = self.build_plan(
            execution_plan("proposed `{0}`.".format(EXISTING_TEST)),
            files={EXISTING_TEST: "package it\n"},
        )

        records = of_kind(batch, KIND_PLANNED_MISSING)
        self.assertEqual(len(records), 1)
        self.assertEqual(records[0].evidence_class, EvidenceClass.GAP_UNKNOWN)
        self.assertTrue(records[0].details["proposed"])
        self.assertEqual(of_kind(batch, KIND_TESTPLAN_EXECUTION), [])

    def test_missing_path_is_a_planned_gap(self):
        _, _, batch = self.build_plan(
            execution_plan("`{0}`.".format(EXISTING_TEST))
        )

        records = of_kind(batch, KIND_PLANNED_MISSING)
        self.assertEqual(len(records), 1)
        self.assertFalse(records[0].details["test_exists"])

    def test_draft_testplan_is_inferred_at_most(self):
        _, _, batch = self.build_plan(
            execution_plan("`{0}`.".format(EXISTING_TEST)),
            approved=False,
            files={EXISTING_TEST: "package it\n"},
        )

        records = of_kind(batch, KIND_TESTPLAN_EXECUTION)
        self.assertEqual(len(records), 1)
        self.assertEqual(records[0].evidence_class, EvidenceClass.INFERRED_MAPPING)
        self.assertEqual(records[0].heuristic, "draft-testplan")
        self.assertEqual(records[0].details["review_status"], "draft")

    def test_matrix_row_supplies_path_and_boundary(self):
        _, _, batch = self.build_plan(
            matrix_plan("`{0}`".format(EXISTING_TEST)),
            files={EXISTING_TEST: "package it\n"},
        )

        record = of_kind(batch, KIND_TESTPLAN_EXECUTION)[0]
        self.assertEqual(record.details["path"], EXISTING_TEST)
        self.assertEqual(record.boundary.real, ("Deployed API, PostgreSQL",))
        self.assertEqual(record.boundary.simulated, ("Provider omitted",))

    def test_boundary_bullet_without_a_simulated_segment_is_real_only(self):
        boundary = (
            "Deployed Fulfillment and PostgreSQL are real. MCP pod restart is "
            "real. Providers may be omitted"
        )
        _, _, batch = self.build_plan(
            execution_plan("`{0}`.".format(EXISTING_TEST), boundary=boundary),
            files={EXISTING_TEST: "package it\n"},
        )

        record = of_kind(batch, KIND_TESTPLAN_EXECUTION)[0]
        self.assertEqual(record.boundary.real, (boundary,))
        self.assertEqual(record.boundary.simulated, ())

    def test_boundary_bullet_explicit_simulated_segment_is_split(self):
        boundary = "Real: deployed API and PostgreSQL. Simulated: provider"
        _, _, batch = self.build_plan(
            execution_plan("`{0}`.".format(EXISTING_TEST), boundary=boundary),
            files={EXISTING_TEST: "package it\n"},
        )

        record = of_kind(batch, KIND_TESTPLAN_EXECUTION)[0]
        self.assertEqual(record.boundary.real, ("deployed API and PostgreSQL",))
        self.assertEqual(record.boundary.simulated, ("provider",))

    def test_execution_command_without_a_path_is_details_only(self):
        _, ctx, batch = self.build_plan(
            execution_plan("`make -C osac-installer test SUITE=fulfillment`.")
        )

        self.assertEqual(of_kind(batch, KIND_TESTPLAN_EXECUTION), [])
        self.assertEqual(of_kind(batch, KIND_PLANNED_MISSING), [])
        self.assertIn("TESTPLAN_EXECUTION_NO_PATH", codes(batch))

    def test_evidence_id_is_stable(self):
        _, ctx, first = self.build_plan(
            execution_plan("`{0}`.".format(EXISTING_TEST)),
            files={EXISTING_TEST: "package it\n"},
        )
        second = TestMappingCollector().collect(ctx)
        self.assertEqual(
            [record.id for record in of_kind(first, KIND_TESTPLAN_EXECUTION)],
            [record.id for record in of_kind(second, KIND_TESTPLAN_EXECUTION)],
        )


class TestExecutionMatrixIds(unittest.TestCase):
    def test_ranges_expand_with_the_starting_width(self):
        self.assertEqual(
            _expand_tc_ids("TC-FR10-01–06, TC-NFR3-01"),
            (
                "TC-FR10-01",
                "TC-FR10-02",
                "TC-FR10-03",
                "TC-FR10-04",
                "TC-FR10-05",
                "TC-FR10-06",
                "TC-NFR3-01",
            ),
        )


REVIEWER_MAPPING = """\
version: 1
feature: OSAC-1111
reviewed_by: {reviewer}
mappings:
  - behavior: TC-FR1-01
    tests:
      - path: fulfillment-service/it/it_disk_images_api_test.go
        name: DiskImage API
        tier: integration
"""


class TestReviewerValidation(MappingFixture):
    def evidence_class(self, reviewer: str):
        _, _, batch = self.build(
            mapping_config=REVIEWER_MAPPING.format(reviewer=reviewer),
            test_files={
                "fulfillment-service/it/it_disk_images_api_test.go": PLAIN_API_TEST
            },
        )
        return of_kind(batch, KIND_CONFIG)[0].evidence_class, batch

    def test_non_string_and_placeholder_reviewers_are_unreviewed(self):
        for reviewer in (
            "[]",
            "false",
            "n/a",
            "pending review",
            '"PENDING-REVIEW"',
            "PENDINGREVIEW",
            "pendingreview",
            '"false"',
            '"yes"',
        ):
            with self.subTest(reviewer=reviewer):
                evidence_class, _ = self.evidence_class(reviewer)
                self.assertEqual(
                    evidence_class, EvidenceClass.INFERRED_MAPPING
                )

    def test_a_bare_user_name_is_not_a_handle(self):
        evidence_class, _ = self.evidence_class("itzik")
        self.assertEqual(evidence_class, EvidenceClass.INFERRED_MAPPING)

    def test_valid_user_and_team_handles_are_reviewed(self):
        for reviewer in ('"@itzik"', "osac-project/qe", '"@osac-project/qe"'):
            with self.subTest(reviewer=reviewer):
                evidence_class, _ = self.evidence_class(reviewer)
                self.assertEqual(
                    evidence_class, EvidenceClass.DECLARED_MAPPING
                )

    def test_non_string_reviewer_is_reported(self):
        _, batch = self.evidence_class("[]")
        self.assertIn("MAPPING_REVIEWER_INVALID", codes(batch))


class TestReviewedConfig(MappingFixture):
    def test_reviewed_config_emits_declared_evidence(self):
        _, _, batch = self.build(
            mapping_config=fixture("mappings", "reviewed.yaml"),
            test_files={"fulfillment-service/it/it_disk_images_api_test.go": PLAIN_API_TEST},
        )

        records = of_kind(batch, KIND_CONFIG)
        self.assertEqual(len(records), 1)
        record = records[0]
        self.assertEqual(record.evidence_class, EvidenceClass.DECLARED_MAPPING)
        self.assertEqual(record.subject, "OSAC-1111/TC-FR1-01")
        self.assertEqual(record.feature, "OSAC-1111")
        self.assertEqual(record.tier, Tier.INTEGRATION)
        self.assertEqual(record.collector, COLLECTOR_NAME)
        self.assertIsNone(record.confidence)
        self.assertIsNone(record.heuristic)
        self.assertEqual(record.boundary.real, ("fulfillment-api", "postgresql"))
        self.assertEqual(record.boundary.simulated, ())
        self.assertEqual(
            record.details["path"], "fulfillment-service/it/it_disk_images_api_test.go"
        )
        self.assertEqual(record.details["name"], "DiskImage API")
        self.assertEqual(record.details["reviewed_by"], "@qe-team")
        self.assertTrue(record.details["test_exists"])
        self.assertTrue(record.id.startswith("ev-"))

    def test_evidence_id_is_stable_across_runs(self):
        _, ctx, first = self.build(
            mapping_config=fixture("mappings", "reviewed.yaml"),
            test_files={"fulfillment-service/it/it_disk_images_api_test.go": PLAIN_API_TEST},
        )
        second = TestMappingCollector().collect(ctx)
        self.assertEqual(
            [record.id for record in of_kind(first, KIND_CONFIG)],
            [record.id for record in of_kind(second, KIND_CONFIG)],
        )

    def test_pending_review_config_is_inferred(self):
        _, _, batch = self.build(
            mapping_config=fixture("mappings", "pending.yaml"),
            test_files={"fulfillment-service/it/it_disk_images_api_test.go": PLAIN_API_TEST},
        )

        record = of_kind(batch, KIND_CONFIG)[0]
        self.assertEqual(record.evidence_class, EvidenceClass.INFERRED_MAPPING)
        self.assertEqual(record.heuristic, "unreviewed-config")
        self.assertAlmostEqual(record.confidence, 0.7)
        self.assertEqual(record.details["review_status"], "unreviewed")

    def test_missing_reviewed_by_is_unreviewed(self):
        config = (
            "version: 1\n"
            "feature: OSAC-1111\n"
            "mappings:\n"
            "  - behavior: TC-FR1-01\n"
            "    tests:\n"
            "      - path: fulfillment-service/it/it_disk_images_api_test.go\n"
            "        name: DiskImage API\n"
            "        tier: integration\n"
        )
        _, _, batch = self.build(
            mapping_config=config,
            test_files={"fulfillment-service/it/it_disk_images_api_test.go": PLAIN_API_TEST},
        )
        record = of_kind(batch, KIND_CONFIG)[0]
        self.assertEqual(record.evidence_class, EvidenceClass.INFERRED_MAPPING)
        self.assertEqual(record.heuristic, "unreviewed-config")

    def test_missing_test_path_is_gap_unknown(self):
        _, _, batch = self.build(
            mapping_config=fixture("mappings", "missing-path.yaml"),
        )

        records = of_kind(batch, KIND_MISSING_TEST)
        self.assertEqual(len(records), 1)
        record = records[0]
        self.assertEqual(record.evidence_class, EvidenceClass.GAP_UNKNOWN)
        self.assertEqual(record.tier, Tier.INTEGRATION)
        self.assertFalse(record.details["test_exists"])
        self.assertIn(
            "it_disk_images_removed_test.go", record.details["configured_path"]
        )

        diagnostic = diagnostic_with(batch, "MAPPING_TEST_MISSING")
        self.assertEqual(diagnostic.severity, "warning")

    def test_unknown_behavior_is_an_error(self):
        _, _, batch = self.build(
            mapping_config=fixture("mappings", "unknown-behavior.yaml"),
            test_files={"fulfillment-service/it/it_disk_images_api_test.go": PLAIN_API_TEST},
        )

        self.assertEqual(of_kind(batch, KIND_CONFIG), [])
        diagnostic = diagnostic_with(batch, "MAPPING_BEHAVIOR_UNKNOWN")
        self.assertEqual(diagnostic.severity, "error")
        self.assertIn("TC-FR99-01", diagnostic.message)

    def test_invalid_version_is_rejected(self):
        _, _, batch = self.build(
            mapping_config=fixture("mappings", "invalid-version.yaml"),
            test_files={"fulfillment-service/it/it_disk_images_api_test.go": PLAIN_API_TEST},
        )

        self.assertEqual(of_kind(batch, KIND_CONFIG), [])
        diagnostic = diagnostic_with(batch, "MAPPING_CONFIG_INVALID")
        self.assertEqual(diagnostic.severity, "error")

    def test_untiered_unknown_path_is_skipped(self):
        config = (
            "version: 1\n"
            "feature: OSAC-1111\n"
            "reviewed_by: \"@qe-team\"\n"
            "mappings:\n"
            "  - behavior: TC-FR1-01\n"
            "    tests:\n"
            "      - path: docs/whatever.md\n"
            "        name: doc\n"
        )
        _, _, batch = self.build(mapping_config=config)

        self.assertEqual(of_kind(batch, KIND_CONFIG), [])
        self.assertEqual(of_kind(batch, KIND_MISSING_TEST), [])
        diagnostic_with(batch, "MAPPING_TIER_UNKNOWN")


class TestTestCodeReferences(MappingFixture):
    def test_anchored_tc_reference_is_declared(self):
        source = """package it

var _ = Describe("DiskImage API", func() {
	It("creates a DiskImage (OSAC-1111 TC-FR1-01)", func() {
	})
})
"""
        _, _, batch = self.build(
            test_files={"fulfillment-service/it/it_disk_images_api_test.go": source}
        )

        records = of_kind(batch, KIND_TEST_REFERENCE)
        self.assertEqual(len(records), 1)
        record = records[0]
        self.assertEqual(record.evidence_class, EvidenceClass.DECLARED_MAPPING)
        self.assertEqual(record.subject, "OSAC-1111/TC-FR1-01")
        self.assertEqual(record.tier, Tier.INTEGRATION)
        self.assertEqual(record.details["line"], 4)
        self.assertEqual(record.details["occurrences"], 1)
        self.assertIn(record.details["anchored_by"], ("file", "enclosing"))

    def test_description_anchor_is_recorded_as_enclosing(self):
        source = """package it

var _ = Describe("OSAC-1111 DiskImage API", func() {
	It("creates a DiskImage (TC-FR1-01)", func() {
	})
})
"""
        _, _, batch = self.build(
            test_files={"fulfillment-service/it/it_disk_images_api_test.go": source}
        )
        record = of_kind(batch, KIND_TEST_REFERENCE)[0]
        self.assertEqual(record.evidence_class, EvidenceClass.DECLARED_MAPPING)
        self.assertEqual(record.details["anchored_by"], "enclosing")

    def test_unanchored_tc_reference_is_only_inferred(self):
        source = """package it

var _ = Describe("DiskImage API", func() {
	It("creates a DiskImage (TC-FR1-01)", func() {
	})
})
"""
        _, _, batch = self.build(
            test_files={"fulfillment-service/it/it_disk_images_api_test.go": source}
        )

        record = of_kind(batch, KIND_TEST_REFERENCE)[0]
        self.assertEqual(record.evidence_class, EvidenceClass.INFERRED_MAPPING)
        self.assertEqual(record.heuristic, "tc-id-without-feature-anchor")
        self.assertAlmostEqual(record.confidence, 0.4)
        self.assertEqual(record.details["anchor"], "missing")

    def test_tc_id_shared_with_another_feature_is_not_resolved(self):
        source = """package it

var _ = Describe("DiskImage API", func() {
	It("covers TC-FR9-01 which belongs elsewhere", func() {
	})
})
"""
        _, _, batch = self.build(
            test_files={"fulfillment-service/it/it_disk_images_api_test.go": source}
        )

        self.assertEqual(of_kind(batch, KIND_TEST_REFERENCE), [])
        self.assertIn("MAPPING_TEST_CASE_UNKNOWN", codes(batch))

    def test_same_reference_twice_collapses_to_one_record(self):
        source = """package it

var _ = Describe("OSAC-1111 DiskImage API", func() {
	It("creates (TC-FR1-01)", func() {
		// TC-FR1-01 again
	})
})
"""
        _, _, batch = self.build(
            test_files={"fulfillment-service/it/it_disk_images_api_test.go": source}
        )
        records = of_kind(batch, KIND_TEST_REFERENCE)
        self.assertEqual(len(records), 1)
        self.assertEqual(records[0].details["occurrences"], 2)


class TestIntegrationDoc(MappingFixture):
    def test_coverage_note_bullet_is_declared(self):
        _, _, batch = self.build(
            integration_doc=fixture("mappings", "integration-testing.md"),
            test_files={
                "fulfillment-service/it/it_disk_images_api_test.go": PLAIN_API_TEST,
                "tests/e2e/test_disk_image_create.py": "def test_create():\n    pass\n",
            },
        )

        records = of_kind(batch, KIND_COVERAGE_NOTE)
        subjects = sorted(record.subject for record in records)
        self.assertEqual(
            subjects,
            [
                "OSAC-1111/TC-FR1-01",
                "OSAC-1111/TC-FR1-02",
                "OSAC-1111/TC-FR7-04",
            ],
        )
        for record in records:
            self.assertEqual(record.evidence_class, EvidenceClass.DECLARED_MAPPING)
            self.assertIsNone(record.heuristic)
        by_subject = {record.subject: record for record in records}
        self.assertEqual(
            by_subject["OSAC-1111/TC-FR1-01"].tier, Tier.INTEGRATION
        )
        self.assertEqual(
            by_subject["OSAC-1111/TC-FR1-01"].details["path"],
            "fulfillment-service/it/it_disk_images_api_test.go",
        )
        self.assertEqual(by_subject["OSAC-1111/TC-FR7-04"].tier, Tier.E2E)

    def test_bullet_without_the_feature_key_is_ignored(self):
        _, _, batch = self.build(
            integration_doc=fixture("mappings", "integration-testing.md"),
            test_files={"fulfillment-service/it/it_disk_images_api_test.go": PLAIN_API_TEST},
        )
        record = of_kind(batch, KIND_COVERAGE_NOTE)[0]
        self.assertNotIn(
            "it_labels_test.go", str(record.details.get("path", ""))
        )


class TestHeuristics(MappingFixture):
    def test_keyword_overlap_is_never_declared(self):
        source = "def test_disk_image_create_with_required_fields():\n    pass\n"
        _, _, batch = self.build(
            test_files={"tests/e2e/test_disk_image_create.py": source},
        )

        records = of_kind(batch, KIND_KEYWORD)
        self.assertTrue(records)
        for record in records:
            self.assertEqual(record.evidence_class, EvidenceClass.INFERRED_MAPPING)
            self.assertEqual(record.heuristic, "keyword-overlap")
            self.assertIsNotNone(record.confidence)
            self.assertGreater(record.confidence, 0.0)
            self.assertLessEqual(record.confidence, 1.0)
            self.assertEqual(record.details["path"], "tests/e2e/test_disk_image_create.py")

    def test_no_record_exceeds_inferred_without_a_human_source(self):
        source = "def test_disk_image_create_with_required_fields():\n    pass\n"
        _, _, batch = self.build(
            test_files={"tests/e2e/test_disk_image_create.py": source},
        )
        for record in batch.records:
            self.assertIn(
                record.evidence_class,
                (EvidenceClass.INFERRED_MAPPING, EvidenceClass.DECLARED_MAPPING),
            )
            if record.evidence_class is EvidenceClass.INFERRED_MAPPING:
                self.assertIsNotNone(record.heuristic)

    def test_stronger_sources_suppress_the_heuristic_for_the_same_path(self):
        config = (
            "version: 1\n"
            "feature: OSAC-1111\n"
            "reviewed_by: \"@qe-team\"\n"
            "mappings:\n"
            "  - behavior: TC-FR1-01\n"
            "    tests:\n"
            "      - path: tests/e2e/test_disk_image_create.py\n"
            "        name: test_disk_image_create_with_required_fields\n"
            "        tier: e2e\n"
        )
        source = "def test_disk_image_create_with_required_fields():\n    pass\n"
        _, _, batch = self.build(
            mapping_config=config,
            test_files={"tests/e2e/test_disk_image_create.py": source},
        )

        heuristic = [
            record
            for record in of_kind(batch, KIND_KEYWORD)
            if record.details["path"] == "tests/e2e/test_disk_image_create.py"
            and record.subject == "OSAC-1111/TC-FR1-01"
        ]
        self.assertEqual(heuristic, [])


class TestDeterminism(MappingFixture):
    def test_file_creation_order_does_not_change_the_output(self):
        files = {
            "fulfillment-service/it/it_disk_images_api_test.go": PLAIN_API_TEST,
            "fulfillment-service/it/it_disk_image_lifecycle_test.go": (
                "package it\n\nvar _ = Describe(\"DiskImage lifecycle (TC-FR7-04)\", func() {\n})\n"
            ),
            "tests/e2e/test_disk_image_create.py": (
                "def test_disk_image_create_with_required_fields():\n    pass\n"
            ),
        }
        _, _, first = self.build(
            mapping_config=fixture("mappings", "pending.yaml"),
            test_files=files,
        )
        _, _, second = self.build(
            mapping_config=fixture("mappings", "pending.yaml"),
            test_files=dict(reversed(list(files.items()))),
        )

        self.assertEqual(
            [record.to_dict() for record in first.records],
            [record.to_dict() for record in second.records],
        )
        self.assertEqual(
            [record.sort_key() for record in first.records],
            sorted(record.sort_key() for record in first.records),
        )

    def test_repeated_collection_is_identical(self):
        _, ctx, first = self.build(
            mapping_config=fixture("mappings", "reviewed.yaml"),
            test_files={"fulfillment-service/it/it_disk_images_api_test.go": PLAIN_API_TEST},
        )
        second = TestMappingCollector().collect(ctx)
        self.assertEqual(
            [record.to_dict() for record in first.records],
            [record.to_dict() for record in second.records],
        )
        self.assertEqual(
            [diagnostic.to_dict() for diagnostic in first.diagnostics],
            [diagnostic.to_dict() for diagnostic in second.diagnostics],
        )


class TestMissingInventory(MappingFixture):
    def test_no_inventory_produces_no_mapping_records(self):
        _, _, batch = self.build(
            testplan="",
            mapping_config=fixture("mappings", "reviewed.yaml"),
            test_files={"fulfillment-service/it/it_disk_images_api_test.go": PLAIN_API_TEST},
        )

        self.assertEqual(batch.records, ())
        self.assertEqual(batch.collector, COLLECTOR_NAME)

    def test_unavailable_inventory_diagnostic_comes_from_the_behaviors_collector(self):
        _, _, batch = self.build(testplan="")
        self.assertEqual(batch.records, ())
        self.assertNotIn("INVENTORY_UNAVAILABLE", codes(batch))


if __name__ == "__main__":
    unittest.main()
