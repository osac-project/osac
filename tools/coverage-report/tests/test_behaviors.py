"""Contract tests for the behavior-inventory collector."""

import tempfile
import unittest
from pathlib import Path

import path_setup  # noqa: F401  (adds the package root to sys.path)
from coverage_report.collectors import CollectorContext
from coverage_report.collectors.behaviors import (
    COLLECTOR_NAME,
    BehaviorInventoryCollector,
    collect_inventory,
    infer_tier_from_path,
    normalize_testplan_text,
    testplan_revision,
    tier_details,
)
from coverage_report.config import EffectiveConfig
from coverage_report.model import CollectorBatch, Tier
from coverage_report.scope import FeatureEntry

FIXTURES = Path(__file__).resolve().parent / "fixtures" / "behaviors"


def fixture(name: str) -> str:
    return (FIXTURES / name).read_text(encoding="utf-8")


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
        "components": (),
        "unit_packages": (),
        "suites": {},
    }
    values.update(overrides)
    return FeatureEntry(**values)


def make_config(root: Path, enhancement_roots=()) -> EffectiveConfig:
    return EffectiveConfig(
        version=1,
        features_file=str(Path(root, "features.yaml")),
        enhancement_roots=tuple(str(item) for item in enhancement_roots),
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


def make_context(root: Path, features, enhancement_roots=()) -> CollectorContext:
    return CollectorContext(
        repo_root=str(root),
        sha="0" * 40,
        features=tuple(features),
        config=make_config(root, enhancement_roots),
        run_id="run-test",
    )


def codes(diagnostics) -> list:
    return [diagnostic.code for diagnostic in diagnostics]


def diagnostic_with(diagnostics, code):
    for diagnostic in diagnostics:
        if diagnostic.code == code:
            return diagnostic
    raise AssertionError("no diagnostic with code {0} in {1}".format(code, codes(diagnostics)))


class TestplanFixtureCase(unittest.TestCase):
    """Base class that materializes a test plan in a throwaway repository."""

    def build(self, text: str, *, key: str = "OSAC-1111", relative: str = "docs/testplan.md"):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        root = Path(self._tmp.name)
        write_file(root, relative, text)
        feature = make_feature(key=key, testplan=relative)
        ctx = make_context(root, [feature])
        return root, ctx


class TestFormatA(TestplanFixtureCase):
    def test_parses_story_ac_table_and_ids(self):
        _, ctx = self.build(fixture("format-a.md"))
        batch = BehaviorInventoryCollector().collect(ctx)

        self.assertEqual(
            [behavior.id for behavior in batch.behaviors],
            [
                "OSAC-1111/TC-FR1-01",
                "OSAC-1111/TC-FR1-02",
                "OSAC-1111/TC-FR7-04",
                "OSAC-1111/TC-FR7-06",
            ],
        )

        first = batch.behaviors[0]
        self.assertEqual(first.title, "Create DiskImage with required fields")
        self.assertEqual(first.requirement_anchor, "FR-1")
        self.assertEqual(first.priority, "critical")
        self.assertEqual(first.automation, "automated")
        self.assertIsNone(first.required_tier)
        self.assertEqual(first.required_boundary, ())
        self.assertTrue(first.assessable)
        self.assertIsNone(first.exclusion_reason)

    def test_continued_heading_keeps_the_anchor(self):
        _, ctx = self.build(fixture("format-a.md"))
        batch = BehaviorInventoryCollector().collect(ctx)
        continued = [b for b in batch.behaviors if b.id.endswith("TC-FR7-04")][0]
        self.assertEqual(continued.requirement_anchor, "FR-7")

    def test_case_under_a_foreign_requirement_reports_anchor_mismatch(self):
        _, ctx = self.build(fixture("format-a.md"))
        batch = BehaviorInventoryCollector().collect(ctx)

        nested = [b for b in batch.behaviors if b.id.endswith("TC-FR7-06")][0]
        self.assertEqual(nested.requirement_anchor, "FR-13")
        diagnostic = diagnostic_with(batch.diagnostics, "INVENTORY_ANCHOR_MISMATCH")
        self.assertEqual(diagnostic.severity, "info")
        self.assertIn("TC-FR7-06", diagnostic.message)

    def test_requirement_without_cases_produces_no_behavior(self):
        _, ctx = self.build(fixture("format-a.md"))
        batch = BehaviorInventoryCollector().collect(ctx)
        self.assertFalse([b for b in batch.behaviors if "NFR-1" in b.id])

    def test_gaps_section_is_not_parsed(self):
        _, ctx = self.build(fixture("format-a.md"))
        batch = BehaviorInventoryCollector().collect(ctx)
        self.assertEqual(len(batch.behaviors), 4)

    def test_document_without_a_test_cases_section_is_still_parsed(self):
        text = "# Testplan\n\n### FR-1: DiskImage CRUD\n\n#### TC-FR1-01: Create\n\nok\n"
        _, ctx = self.build(text)
        batch = BehaviorInventoryCollector().collect(ctx)
        self.assertEqual([behavior.id for behavior in batch.behaviors], ["OSAC-1111/TC-FR1-01"])


class TestFormatB(TestplanFixtureCase):
    def test_parses_interface_change_table(self):
        _, ctx = self.build(
            fixture("format-b.md"), key="OSAC-2222", relative=".artifacts/design/OSAC-2222/04-testplan.md"
        )
        batch = BehaviorInventoryCollector().collect(ctx)

        self.assertEqual(
            [behavior.id for behavior in batch.behaviors],
            [
                "OSAC-2222/TC-FR1-01",
                "OSAC-2222/TC-FR1-02",
                "OSAC-2222/TC-NFR3-01",
            ],
        )
        first, second, third = batch.behaviors
        self.assertEqual(first.priority, "critical")
        self.assertEqual(first.automation, "automated")
        self.assertEqual(second.priority, "high")
        self.assertEqual(third.requirement_anchor, "NFR-3")
        self.assertTrue(all(behavior.assessable for behavior in batch.behaviors))
        self.assertTrue(all(behavior.required_tier is None for behavior in batch.behaviors))


class TestExplicitTierColumns(TestplanFixtureCase):
    def test_tier_and_boundary_columns(self):
        _, ctx = self.build(fixture("tier-columns.md"), key="OSAC-3333")
        batch = BehaviorInventoryCollector().collect(ctx)

        first, second = batch.behaviors
        self.assertEqual(first.required_tier, Tier.E2E)
        self.assertEqual(first.required_boundary, ("real", "simulated"))
        self.assertEqual(second.required_tier, Tier.INTEGRATION)
        self.assertEqual(second.required_boundary, ())


class TestTierBullets(TestplanFixtureCase):
    def test_tier_owner_and_boundary_bullets(self):
        _, ctx = self.build(fixture("tier-bullets.md"), key="OSAC-4444")
        batch = BehaviorInventoryCollector().collect(ctx)

        first, second = batch.behaviors
        self.assertEqual(first.required_tier, Tier.E2E)
        self.assertEqual(first.required_boundary, ("real",))
        self.assertEqual(second.required_tier, Tier.INTEGRATION)
        self.assertEqual(second.required_boundary, ("real",))


class TestAssessability(TestplanFixtureCase):
    def test_manual_not_automatable_and_out_of_scope_are_unassessable(self):
        _, ctx = self.build(fixture("assessability.md"), key="OSAC-6666")
        batch = BehaviorInventoryCollector().collect(ctx)
        by_suffix = {
            behavior.id.split("/", 1)[1]: behavior for behavior in batch.behaviors
        }

        self.assertFalse(by_suffix["TC-FR5-01"].assessable)
        self.assertIn("manual", by_suffix["TC-FR5-01"].exclusion_reason)
        self.assertFalse(by_suffix["TC-FR5-02"].assessable)
        self.assertIn("not automatable", by_suffix["TC-FR5-02"].exclusion_reason)
        self.assertFalse(by_suffix["TC-FR5-03"].assessable)
        self.assertIn("out of scope", by_suffix["TC-FR5-03"].exclusion_reason)
        self.assertTrue(by_suffix["TC-FR5-04"].assessable)
        self.assertTrue(by_suffix["TC-FR5-04"].exclusion_reason is None)
        self.assertFalse(by_suffix["TC-FR5-05"].assessable)

    def test_unassessable_behaviors_always_carry_a_reason(self):
        _, ctx = self.build(fixture("assessability.md"), key="OSAC-6666")
        batch = BehaviorInventoryCollector().collect(ctx)
        for behavior in batch.behaviors:
            if not behavior.assessable:
                self.assertTrue(behavior.exclusion_reason)


class TestDuplicates(TestplanFixtureCase):
    def test_duplicate_tc_ids_are_reported_and_both_unassessable(self):
        _, ctx = self.build(fixture("duplicate.md"), key="OSAC-5555")
        batch = BehaviorInventoryCollector().collect(ctx)

        self.assertEqual(len(batch.behaviors), 2)
        self.assertTrue(all(not behavior.assessable for behavior in batch.behaviors))
        for behavior in batch.behaviors:
            self.assertIn("duplicate", behavior.exclusion_reason)

        diagnostic = diagnostic_with(batch.diagnostics, "INVENTORY_DUPLICATE")
        self.assertEqual(diagnostic.severity, "error")
        self.assertIn("TC-FR1-01", diagnostic.message)
        self.assertEqual(
            batch.inventories[0].duplicate_ids, ("OSAC-5555/TC-FR1-01",)
        )
        # Every copy is normalized to the same content, so the metrics merge
        # sees one canonical record instead of a conflict.
        self.assertEqual(batch.behaviors[0], batch.behaviors[1])


class TestTierVocabulary(unittest.TestCase):
    def test_labels_map_to_the_primary_tier(self):
        cases = {
            "Unit": Tier.UNIT,
            "Component integration": Tier.INTEGRATION,
            "Component integration / [UI]": Tier.INTEGRATION,
            "Contract": Tier.INTEGRATION,
            "Contract and component integration": Tier.INTEGRATION,
            "UI integration": Tier.INTEGRATION,
            "E2E": Tier.E2E,
            "E2E / [QE].": Tier.E2E,
        }
        for label, expected in cases.items():
            with self.subTest(label=label):
                self.assertIs(tier_details(label)[0], expected)

    def test_mixed_labels_take_the_first_tier_and_note_the_second(self):
        tier, label, note = tier_details(
            "Unit / [DEV] plus component integration for the RPC"
        )
        self.assertIs(tier, Tier.UNIT)
        self.assertEqual(
            label, "Unit / [DEV] plus component integration for the RPC"
        )
        self.assertEqual(note, "component integration for the RPC")

        tier, _, note = tier_details("E2E (manual) + unit")
        self.assertIs(tier, Tier.E2E)
        self.assertEqual(note, "unit")

    def test_a_story_id_is_not_a_secondary_tier(self):
        tier, label, note = tier_details("Contract / [DEV], Story 8.05")
        self.assertIs(tier, Tier.INTEGRATION)
        self.assertEqual(label, "Contract / [DEV], Story 8.05")
        self.assertIsNone(note)

    def test_unknown_label_has_no_tier(self):
        self.assertEqual(tier_details("sanity"), (None, "sanity", None))


class TestTierOverrides(TestplanFixtureCase):
    def build_with_tiers(self, behavior_tiers: str):
        root, ctx = self.build(fixture("tier-columns.md"), key="OSAC-1111")
        write_file(
            root,
            "config/mappings/OSAC-1111.yaml",
            "version: 1\nfeature: OSAC-1111\nreviewed_by: \"{0}\"\n"
            "behavior_tiers:\n{1}".format("@qe-team", behavior_tiers),
        )
        return collect_inventory(ctx)[0]

    def behavior(self, inventory, tc_id):
        for behavior in inventory.behaviors:
            if behavior.id == "OSAC-1111/{0}".format(tc_id):
                return behavior
        raise AssertionError("no behavior {0}".format(tc_id))

    def test_mapping_override_wins_over_the_testplan(self):
        inventory = self.build_with_tiers("  TC-FR2-01: integration\n")

        overridden = self.behavior(inventory, "TC-FR2-01")
        self.assertIs(overridden.required_tier, Tier.INTEGRATION)
        self.assertEqual(overridden.tier_origin, "mapping-config")
        self.assertTrue(overridden.tier_reviewed)

        from_testplan = self.behavior(inventory, "TC-FR2-02")
        self.assertIs(from_testplan.required_tier, Tier.INTEGRATION)
        self.assertEqual(from_testplan.tier_origin, "testplan")
        self.assertTrue(from_testplan.tier_reviewed)

    def test_pending_review_override_applies_but_is_flagged(self):
        root, ctx = self.build(fixture("tier-columns.md"), key="OSAC-1111")
        write_file(
            root,
            "config/mappings/OSAC-1111.yaml",
            "version: 1\nfeature: OSAC-1111\nreviewed_by: PENDING-REVIEW\n"
            "behavior_tiers:\n  TC-FR2-01: integration\n",
        )
        inventory = collect_inventory(ctx)[0]

        overridden = self.behavior(inventory, "TC-FR2-01")
        self.assertIs(overridden.required_tier, Tier.INTEGRATION)
        self.assertEqual(overridden.tier_origin, "mapping-config")
        self.assertFalse(overridden.tier_reviewed)

    def test_invalid_tier_is_reported_and_the_testplan_tier_survives(self):
        inventory = self.build_with_tiers("  TC-FR2-01: sanity\n")

        overridden = self.behavior(inventory, "TC-FR2-01")
        self.assertIs(overridden.required_tier, Tier.E2E)
        self.assertEqual(overridden.tier_origin, "testplan")
        self.assertIn("TIER_OVERRIDE_INVALID", codes(inventory.diagnostics))

    def test_malformed_mapping_file_is_reported(self):
        root, ctx = self.build(fixture("tier-columns.md"), key="OSAC-1111")
        write_file(root, "config/mappings/OSAC-1111.yaml", "not: [valid")

        inventory = collect_inventory(ctx)[0]

        self.assertIn("MAPPING_CONFIG_INVALID", codes(inventory.diagnostics))

    def test_non_mapping_file_is_reported(self):
        root, ctx = self.build(fixture("tier-columns.md"), key="OSAC-1111")
        write_file(root, "config/mappings/OSAC-1111.yaml", "- just\n- a list\n")

        inventory = collect_inventory(ctx)[0]

        self.assertIn("MAPPING_CONFIG_INVALID", codes(inventory.diagnostics))

    def test_unknown_override_behavior_is_reported(self):
        inventory = self.build_with_tiers("  TC-FR9-99: integration\n")

        self.assertIn(
            "TIER_OVERRIDE_UNKNOWN_BEHAVIOR", codes(inventory.diagnostics)
        )


class TestMissingInventory(unittest.TestCase):
    def test_missing_testplan_emits_unavailable_and_no_behaviors(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            ctx = make_context(root, [make_feature()])
            batch = BehaviorInventoryCollector().collect(ctx)

        self.assertEqual(batch.behaviors, ())
        self.assertEqual(batch.records, ())
        diagnostic = diagnostic_with(batch.diagnostics, "INVENTORY_UNAVAILABLE")
        self.assertEqual(diagnostic.severity, "warning")
        self.assertIn("OSAC-1111", diagnostic.message)
        self.assertIn("docs/testplan.md", diagnostic.message)
        self.assertEqual(diagnostic.recovery, diagnostic.recovery.strip())
        self.assertTrue(diagnostic.recovery)

    def test_missing_inventory_never_produces_a_zero_count_behavior(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            ctx = make_context(Path(tmpdir), [make_feature()])
            inventory = collect_inventory(ctx)[0]

        self.assertEqual(inventory.behaviors, ())
        self.assertIsNone(inventory.revision)
        self.assertIsNone(inventory.path)


class TestEnhancementRootFallback(unittest.TestCase):
    def test_prefixed_testplan_is_found_in_an_enhancement_root(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            write_file(
                root,
                "enhancement-proposals/enhancements/OSAC-1111-disk-image/testplan.md",
                fixture("format-a.md"),
            )
            ctx = make_context(
                root,
                [make_feature(testplan=None)],
                enhancement_roots=[root / "enhancement-proposals" / "enhancements"],
            )
            inventory = collect_inventory(ctx)[0]

        self.assertEqual(len(inventory.behaviors), 4)
        self.assertTrue(inventory.path.endswith("OSAC-1111-disk-image/testplan.md"))

    def test_design_artifact_is_found_by_key_directory(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            write_file(
                root,
                ".artifacts/design/OSAC-2222/04-testplan.md",
                fixture("format-b.md"),
            )
            ctx = make_context(
                root,
                [make_feature(key="OSAC-2222", testplan=None)],
                enhancement_roots=[root / ".artifacts" / "design"],
            )
            inventory = collect_inventory(ctx)[0]

        self.assertEqual(len(inventory.behaviors), 3)

    def test_fallback_choice_is_deterministic(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            write_file(
                root,
                "enhancement-proposals/enhancements/OSAC-1111-b/testplan.md",
                fixture("format-a.md"),
            )
            write_file(
                root,
                "enhancement-proposals/enhancements/OSAC-1111-a/testplan.md",
                fixture("format-a.md"),
            )
            ctx = make_context(
                root,
                [make_feature(testplan=None)],
                enhancement_roots=[root / "enhancement-proposals" / "enhancements"],
            )
            first = collect_inventory(ctx)[0]
            second = collect_inventory(ctx)[0]

        self.assertEqual(first.path, second.path)
        self.assertTrue(first.path.endswith("OSAC-1111-a/testplan.md"))


class TestInventoryMetadata(TestplanFixtureCase):
    def test_inventory_metadata_names_path_revision_and_count(self):
        _, ctx = self.build(fixture("format-a.md"))
        batch = BehaviorInventoryCollector().collect(ctx)

        self.assertEqual(len(batch.inventories), 1)
        inventory = batch.inventories[0]
        self.assertEqual(inventory.feature, "OSAC-1111")
        self.assertEqual(inventory.path, "docs/testplan.md")
        self.assertEqual(inventory.behavior_count, 4)
        self.assertEqual(inventory.duplicate_ids, ())
        self.assertRegex(inventory.revision or "", r"^[0-9a-f]{64}$")
        self.assertEqual(
            [diagnostic.code for diagnostic in batch.diagnostics].count(
                "INVENTORY_LOADED"
            ),
            0,
        )

    def test_revision_ignores_line_endings_and_trailing_spaces(self):
        text = fixture("format-a.md")
        crlf = text.replace("\n", "  \r\n")
        self.assertEqual(testplan_revision(text), testplan_revision(crlf))
        self.assertEqual(normalize_testplan_text("\r\n"), "\n")

    def test_revision_changes_with_content(self):
        text = fixture("format-a.md")
        changed = text.replace("critical", "high", 1)
        self.assertNotEqual(testplan_revision(text), testplan_revision(changed))

    def test_inventory_is_not_emitted_as_evidence(self):
        _, ctx = self.build(fixture("format-a.md"))
        batch = BehaviorInventoryCollector().collect(ctx)
        self.assertIsInstance(batch, CollectorBatch)
        self.assertEqual(batch.records, ())
        self.assertEqual(batch.collector, COLLECTOR_NAME)

    def test_testplan_without_cases_reports_a_warning(self):
        _, ctx = self.build("# Testplan\n\n## Test Cases\n\nNothing here yet.\n")
        batch = BehaviorInventoryCollector().collect(ctx)
        self.assertEqual(batch.behaviors, ())
        diagnostic_with(batch.diagnostics, "INVENTORY_NO_TEST_CASES")


class TestStability(TestplanFixtureCase):
    def test_behavior_ids_are_namespaced_and_sorted(self):
        _, ctx = self.build(fixture("format-a.md"))
        first = BehaviorInventoryCollector().collect(ctx)
        second = BehaviorInventoryCollector().collect(ctx)

        ids = [behavior.id for behavior in first.behaviors]
        self.assertEqual(ids, sorted(ids))
        for behavior_id in ids:
            self.assertTrue(behavior_id.startswith("OSAC-1111/TC-"))
        self.assertEqual(ids, [behavior.id for behavior in second.behaviors])

    def test_diagnostics_are_sorted_across_runs(self):
        _, ctx = self.build(fixture("format-a.md"))
        first = BehaviorInventoryCollector().collect(ctx)
        second = BehaviorInventoryCollector().collect(ctx)
        self.assertEqual(codes(first.diagnostics), codes(second.diagnostics))
        self.assertEqual(
            [d.to_dict() for d in first.diagnostics],
            [d.to_dict() for d in second.diagnostics],
        )


class TestTierInference(unittest.TestCase):
    def test_path_inference(self):
        self.assertEqual(infer_tier_from_path("tests/e2e/vmaas/test_x.py"), Tier.E2E)
        self.assertEqual(
            infer_tier_from_path("fulfillment-service/it/it_x_test.go"), Tier.INTEGRATION
        )
        self.assertEqual(
            infer_tier_from_path("osac-operator/internal/controller/x_test.go"), Tier.UNIT
        )
        self.assertIsNone(infer_tier_from_path("docs/guide.md"))


if __name__ == "__main__":
    unittest.main()
