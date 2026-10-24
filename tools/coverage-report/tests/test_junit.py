"""Tests for the JUnit execution-evidence collector."""

import json
import tempfile
import unittest
from pathlib import Path

import path_setup  # noqa: F401  (adds the package root to sys.path)

from coverage_report.collectors import CollectorContext
from coverage_report.collectors.junit import (
    COLLECTOR_NAME,
    JunitCollector,
    normalize_test_identity,
)
from coverage_report.config import EffectiveConfig
from coverage_report.model import EvidenceClass, Freshness, Tier
from coverage_report.scope import FeatureEntry

FIXTURES = Path(__file__).resolve().parent / "fixtures" / "junit"

SHA = "0123456789abcdef0123456789abcdef01234567"
OTHER_SHA = "fedcba9876543210fedcba9876543210fedcba98"

FEATURES = (
    FeatureEntry(key="OSAC-2540", name="disk-image", aliases=("diskimage",)),
    FeatureEntry(key="OSAC-3000", name="virtual-machine", aliases=("vm",)),
)


def simple_suite_xml(
    name="[It] DiskImage creates an image", outcome="passed", revision=SHA
):
    """Return a one-testcase Ginkgo-shaped testsuite document."""
    if outcome == "failed":
        body = '<failure message="boom">boom</failure>'
    elif outcome == "skipped":
        body = '<skipped message="pending"/>'
    else:
        body = ""
    properties = ""
    if revision:
        properties = (
            "<properties>"
            "<property name=\"revision\" value=\"{0}\"/>"
            "</properties>"
        ).format(revision)
    return (
        "<testsuite name=\"DiskImage API\">{0}"
        "<testcase classname=\"DiskImage API\" name=\"{1}\" time=\"0.1\">{2}"
        "</testcase></testsuite>"
    ).format(properties, name, body)


class JunitTestCase(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.repo = Path(self._tmp.name)
        (self.repo / "config").mkdir(parents=True)
        (self.repo / "config" / "features.yaml").write_text(
            "version: 1\nfeatures: []\n", encoding="utf-8"
        )

    def make_config(self, junit=()):
        return EffectiveConfig(
            version=1,
            features_file=str(self.repo / "config" / "features.yaml"),
            enhancement_roots=(),
            coverprofiles=(),
            junit=tuple(junit),
            metric_versions={
                "unit": "statement-weighted-v2",
                "integration": "behavior-inventory-v1",
                "e2e": "behavior-inventory-v1",
            },
            output_dir=str(self.repo / ".artifacts" / "coverage-report"),
            origins={},
        )

    def make_ctx(self, *, sha=SHA, junit=(), explicit=None, features=FEATURES):
        return CollectorContext(
            repo_root=str(self.repo),
            sha=sha,
            features=features,
            config=self.make_config(junit=junit),
            run_id="run-test",
            explicit_inputs={"junit": tuple(explicit or ())},
        )

    def collect(self, **kwargs):
        return JunitCollector().collect(self.make_ctx(**kwargs))

    def collect_files(self, **kwargs):
        """Collect with every XML file in the fixture repo as an explicit input."""
        paths = sorted(str(path) for path in self.repo.rglob("*.xml"))
        kwargs.setdefault("explicit", paths)
        return self.collect(**kwargs)

    def write_text(self, relative, text):
        path = self.repo / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")
        return path

    def write_fixture(self, relative, fixture_name, revision=SHA):
        text = (FIXTURES / fixture_name).read_text(encoding="utf-8")
        return self.write_text(relative, text.replace("REVISION_PLACEHOLDER", revision))

    def write_sidecar(self, xml_path, **metadata):
        sidecar = Path(str(xml_path) + ".meta.json")
        sidecar.write_text(json.dumps(metadata), encoding="utf-8")
        return sidecar

    def codes(self, batch):
        return [diagnostic.code for diagnostic in batch.diagnostics]

    def records_by_subject(self, batch):
        return {record.subject: record for record in batch.records}


class TestNormalizeTestIdentity(unittest.TestCase):
    def test_strips_ginkgo_prefix_and_collapses_whitespace(self):
        identity = normalize_test_identity(
            "DiskImage   API", "[It]   DiskImage API creates  an image"
        )
        self.assertEqual(
            identity, "DiskImage API::DiskImage API creates an image"
        )

    def test_prefix_match_is_case_insensitive(self):
        self.assertEqual(
            normalize_test_identity("suite", "[it] test"), "suite::test"
        )

    def test_empty_suite_returns_name_only(self):
        self.assertEqual(normalize_test_identity("", "[It] test"), "test")

    def test_identity_is_stable_for_equivalent_input(self):
        self.assertEqual(
            normalize_test_identity("suite", "test"),
            normalize_test_identity("suite", "[It] test"),
        )


class TestGinkgoReport(JunitTestCase):
    def test_pass_fail_skip_are_observed_execution(self):
        self.write_fixture(
            "fulfillment-service/it/reports/ginkgo.xml", "ginkgo-integration.xml"
        )

        batch = self.collect_files()

        self.assertEqual(len(batch.records), 3)
        self.assertEqual(batch.diagnostics, ())
        for record in batch.records:
            self.assertEqual(record.collector, COLLECTOR_NAME)
            self.assertEqual(record.kind, "junit-testcase")
            self.assertEqual(record.evidence_class, EvidenceClass.OBSERVED_EXECUTION)
            self.assertEqual(record.tier, Tier.INTEGRATION)
            self.assertEqual(record.feature, "OSAC-2540")
            self.assertEqual(record.revision, SHA)
            self.assertEqual(record.freshness, Freshness.CURRENT)
            self.assertEqual(
                record.source, "fulfillment-service/it/reports/ginkgo.xml"
            )
            self.assertEqual(record.timestamp, "2026-10-20T10:00:00Z")
            self.assertNotIn("[It]", record.subject)

        by_subject = self.records_by_subject(batch)
        created = by_subject[
            "DiskImage API::DiskImage API creates a disk image"
        ]
        failed = by_subject[
            "DiskImage API::DiskImage API rejects a duplicate name"
        ]
        skipped = by_subject[
            "DiskImage API::DiskImage API skips the slow provider path"
        ]

        self.assertEqual(created.details["outcome"], "passed")
        self.assertEqual(created.details["duration_s"], 0.5)
        self.assertEqual(failed.details["outcome"], "failed")
        self.assertIn("expected status 409", failed.details["message"])
        self.assertEqual(skipped.details["outcome"], "skipped")

    def test_passing_result_is_never_measured(self):
        self.write_fixture(
            "fulfillment-service/it/reports/ginkgo.xml", "ginkgo-integration.xml"
        )

        batch = self.collect_files()

        self.assertTrue(
            all(
                record.evidence_class is EvidenceClass.OBSERVED_EXECUTION
                for record in batch.records
            )
        )
        self.assertNotIn(
            EvidenceClass.MEASURED,
            {record.evidence_class for record in batch.records},
        )


class TestPytestReport(JunitTestCase):
    def test_e2e_tier_and_alias_attribution(self):
        self.write_fixture("tests/e2e/reports/pytest.xml", "pytest-e2e.xml")

        batch = self.collect_files()

        self.assertEqual(len(batch.records), 2)
        for record in batch.records:
            self.assertEqual(record.tier, Tier.E2E)
            self.assertEqual(record.feature, "OSAC-3000")
            self.assertEqual(record.freshness, Freshness.CURRENT)
            self.assertTrue(record.subject.startswith("tests.e2e.vm."))

        created = self.records_by_subject(batch)[
            "tests.e2e.vm.test_lifecycle::test_vm_create"
        ]
        self.assertEqual(created.details["file"], "tests/e2e/vm/test_lifecycle.py")
        self.assertEqual(created.details["line"], 12)
        self.assertEqual(created.details["duration_s"], 1.5)
        self.assertEqual(created.timestamp, "2026-10-20T11:00:00Z")

        self.assertEqual(self.codes(batch), ["EXECUTION_UNATTRIBUTED"])

    def test_mapping_file_attributes_by_path(self):
        self.write_fixture("tests/e2e/reports/pytest.xml", "pytest-e2e.xml")
        self.write_text(
            "config/mappings/OSAC-3000.yaml",
            "\n".join(
                (
                    "version: 1",
                    "feature: OSAC-3000",
                    "mappings:",
                    "  - behavior: cluster-lifecycle",
                    "    tests:",
                    "      - path: tests/e2e/cluster/test_cluster.py",
                    "        name: test_cluster_create",
                    "        tier: e2e",
                    "",
                )
            ),
        )

        batch = self.collect_files()

        self.assertEqual(len(batch.records), 3)
        self.assertNotIn("EXECUTION_UNATTRIBUTED", self.codes(batch))
        skipped = self.records_by_subject(batch)[
            "tests.e2e.cluster.test_cluster::test_cluster_create"
        ]
        self.assertEqual(skipped.details["outcome"], "skipped")


class TestOutcomes(JunitTestCase):
    def test_unrecognized_status_is_unknown_not_passed(self):
        self.write_text(
            "tests/e2e/reports/odd.xml",
            "<testsuite name=\"s\">"
            "<testcase classname=\"tests.e2e.x\" name=\"OSAC-2540 odd\" "
            "status=\"notrun\"/></testsuite>",
        )

        batch = self.collect_files()

        self.assertEqual(len(batch.records), 1)
        self.assertEqual(batch.records[0].details["outcome"], "unknown")

    def test_missing_status_is_a_pass(self):
        self.write_text(
            "tests/e2e/reports/plain.xml",
            "<testsuite name=\"s\">"
            "<testcase classname=\"tests.e2e.x\" name=\"OSAC-2540 plain\"/>"
            "</testsuite>",
        )

        batch = self.collect_files()

        self.assertEqual(batch.records[0].details["outcome"], "passed")


class TestTierInference(JunitTestCase):
    def test_internal_go_package_is_unit(self):
        self.write_text(
            "coverage/reports/go.xml",
            "<testsuite name=\"registry\">"
            "<testcase classname=\"github.com/osac-project/osac/"
            "fulfillment-service/internal/registry\" name=\"TestCreate\" "
            "time=\"0.02\"/></testsuite>",
        )
        self.write_text(
            "config/mappings/OSAC-2540.yaml",
            "version: 1\nfeature: OSAC-2540\n"
            "mappings:\n  - tests:\n      - name: TestCreate\n",
        )

        batch = self.collect_files()

        self.assertEqual(len(batch.records), 1)
        record = batch.records[0]
        self.assertEqual(record.tier, Tier.UNIT)
        self.assertEqual(record.feature, "OSAC-2540")
        self.assertEqual(
            record.subject,
            "github.com/osac-project/osac/fulfillment-service/internal/registry"
            "::TestCreate",
        )

    def test_filename_convention_supplies_the_tier(self):
        self.write_text(
            "reports/run.unit.xml",
            "<testsuite name=\"pytest\">"
            "<properties><property name=\"revision\" value=\"{0}\"/></properties>"
            "<testcase classname=\"mystery.pkg\" name=\"OSAC-2540 works\"/>"
            "</testsuite>".format(SHA),
        )

        batch = self.collect_files()

        self.assertEqual(len(batch.records), 1)
        self.assertEqual(batch.records[0].tier, Tier.UNIT)
        self.assertEqual(batch.records[0].feature, "OSAC-2540")

    def test_unknown_tier_is_skipped_with_a_diagnostic(self):
        self.write_text(
            "reports/run.xml",
            "<testsuite name=\"pytest\">"
            "<properties><property name=\"revision\" value=\"{0}\"/></properties>"
            "<testcase classname=\"mystery.pkg\" name=\"OSAC-2540 works\"/>"
            "</testsuite>".format(SHA),
        )

        batch = self.collect_files()

        self.assertEqual(batch.records, ())
        self.assertEqual(self.codes(batch), ["EXECUTION_TIER_UNKNOWN"])
        self.assertNotIn("EXECUTION_UNATTRIBUTED", self.codes(batch))


class TestDiscovery(JunitTestCase):
    def test_absent_input_is_unavailable_not_untested(self):
        batch = self.collect()

        self.assertEqual(batch.records, ())
        self.assertEqual(len(batch.diagnostics), 1)
        diagnostic = batch.diagnostics[0]
        self.assertEqual(diagnostic.code, "EXECUTION_EVIDENCE_UNAVAILABLE")
        self.assertEqual(diagnostic.severity, "warning")
        self.assertEqual(diagnostic.operation, "discover")

    def test_configured_globs_find_and_sort_reports(self):
        self.write_text(
            "reports/b.unit.xml", simple_suite_xml(name="[It] DiskImage b")
        )
        self.write_text(
            "reports/a.unit.xml", simple_suite_xml(name="[It] DiskImage a")
        )

        batch = self.collect(junit=(str(self.repo / "reports" / "*.unit.xml"),))

        self.assertEqual(len(batch.records), 2)
        self.assertEqual(
            [record.source for record in batch.records],
            ["reports/a.unit.xml", "reports/b.unit.xml"],
        )

    def test_explicit_inputs_win_over_configured_globs(self):
        self.write_text(
            "reports/a.unit.xml", simple_suite_xml(name="[It] DiskImage a")
        )
        explicit = self.write_text(
            "other/b.unit.xml", simple_suite_xml(name="[It] DiskImage b")
        )

        batch = self.collect(
            junit=(str(self.repo / "reports" / "*.unit.xml"),),
            explicit=[str(explicit)],
        )

        self.assertEqual(len(batch.records), 1)
        self.assertEqual(batch.records[0].source, "other/b.unit.xml")


class TestUnreadableInputs(JunitTestCase):
    def test_missing_explicit_input_is_reported(self):
        missing = str(self.repo / "missing.xml")

        batch = self.collect(explicit=[missing])

        self.assertEqual(batch.records, ())
        self.assertEqual(self.codes(batch), ["JUNIT_UNREADABLE"])
        self.assertIn("missing.xml", batch.diagnostics[0].message)

    def test_malformed_file_does_not_drop_valid_evidence(self):
        self.write_fixture(
            "fulfillment-service/it/reports/ginkgo.xml", "ginkgo-integration.xml"
        )
        self.write_text("reports/bad.xml", "<testsuite><testcase")

        batch = self.collect_files()

        self.assertEqual(len(batch.records), 3)
        self.assertEqual(self.codes(batch), ["JUNIT_UNREADABLE"])
        self.assertIn("reports/bad.xml", batch.diagnostics[0].message)

    def test_unexpected_root_is_unreadable(self):
        self.write_text("reports/odd.xml", "<coverage><packages/></coverage>")

        batch = self.collect_files()

        self.assertEqual(batch.records, ())
        self.assertEqual(self.codes(batch), ["JUNIT_UNREADABLE"])

    def test_doctype_is_rejected_before_parsing(self):
        self.write_text(
            "reports/evil.xml",
            "<?xml version=\"1.0\"?>\n"
            "<!DOCTYPE testsuite [\n"
            "  <!ENTITY xxe SYSTEM \"file:///etc/passwd\">\n"
            "]>\n"
            "<testsuite name=\"pytest\"><testcase classname=\"mystery.pkg\" "
            "name=\"&xxe;\"/></testsuite>",
        )

        batch = self.collect_files()

        self.assertEqual(batch.records, ())
        self.assertEqual(self.codes(batch), ["JUNIT_REJECTED"])
        self.assertIn("reports/evil.xml", batch.diagnostics[0].message)

    def test_entity_declaration_without_doctype_is_rejected(self):
        self.write_text(
            "reports/entity.xml",
            "<testsuite name=\"pytest\"><!ENTITY leak \"secret\"/>"
            "<testcase classname=\"mystery.pkg\" name=\"leak\"/></testsuite>",
        )

        batch = self.collect_files()

        self.assertEqual(batch.records, ())
        self.assertEqual(self.codes(batch), ["JUNIT_REJECTED"])


class TestProvenance(JunitTestCase):
    def test_matching_revision_is_current(self):
        self.write_fixture(
            "fulfillment-service/it/reports/ginkgo.xml", "ginkgo-integration.xml"
        )

        batch = self.collect_files()

        self.assertEqual(
            {record.freshness for record in batch.records}, {Freshness.CURRENT}
        )

    def test_abbreviated_revision_prefix_is_current(self):
        self.write_fixture(
            "fulfillment-service/it/reports/ginkgo.xml",
            "ginkgo-integration.xml",
            revision=SHA[:12],
        )

        batch = self.collect_files()

        self.assertEqual(
            {record.freshness for record in batch.records}, {Freshness.CURRENT}
        )

    def test_too_short_a_prefix_is_stale(self):
        self.write_fixture(
            "fulfillment-service/it/reports/ginkgo.xml",
            "ginkgo-integration.xml",
            revision=SHA[:6],
        )

        batch = self.collect_files()

        self.assertEqual(
            {record.freshness for record in batch.records}, {Freshness.STALE}
        )

    def test_different_revision_is_stale(self):
        self.write_fixture(
            "fulfillment-service/it/reports/ginkgo.xml",
            "ginkgo-integration.xml",
            revision=OTHER_SHA,
        )

        batch = self.collect_files()

        self.assertEqual(
            {record.freshness for record in batch.records}, {Freshness.STALE}
        )
        self.assertNotIn("EXECUTION_REVISION_UNKNOWN", self.codes(batch))

    def test_absent_revision_is_unknown_with_a_diagnostic(self):
        self.write_text(
            "fulfillment-service/it/reports/norev.xml",
            simple_suite_xml(revision=None),
        )

        batch = self.collect_files()

        self.assertEqual(len(batch.records), 1)
        self.assertEqual(batch.records[0].freshness, Freshness.UNKNOWN)
        self.assertIsNone(batch.records[0].revision)
        self.assertEqual(self.codes(batch), ["EXECUTION_REVISION_UNKNOWN"])

    def test_sidecar_supplies_revision_and_ci_metadata(self):
        path = self.write_text(
            "fulfillment-service/it/reports/norev.xml",
            simple_suite_xml(revision=None),
        )
        self.write_sidecar(
            path,
            revision=SHA,
            ci_run_id="run-17",
            ci_url="https://ci.example/run-17",
            generated_at="2026-10-21T00:00:00Z",
        )

        batch = self.collect_files()

        record = batch.records[0]
        self.assertEqual(record.revision, SHA)
        self.assertEqual(record.freshness, Freshness.CURRENT)
        self.assertEqual(record.timestamp, "2026-10-21T00:00:00Z")
        self.assertEqual(record.details["ci_run_id"], "run-17")
        self.assertEqual(record.details["ci_url"], "https://ci.example/run-17")
        self.assertEqual(batch.diagnostics, ())

    def test_sidecar_revision_wins_over_suite_properties(self):
        path = self.write_text(
            "fulfillment-service/it/reports/mixed.xml",
            simple_suite_xml(revision=OTHER_SHA),
        )
        self.write_sidecar(path, revision=SHA)

        batch = self.collect_files()

        self.assertEqual(batch.records[0].revision, SHA)
        self.assertEqual(batch.records[0].freshness, Freshness.CURRENT)

    def test_unreadable_sidecar_is_reported(self):
        path = self.write_text(
            "fulfillment-service/it/reports/norev.xml",
            simple_suite_xml(revision=None),
        )
        Path(str(path) + ".meta.json").write_text("{not json", encoding="utf-8")

        batch = self.collect_files()

        self.assertEqual(batch.records[0].freshness, Freshness.UNKNOWN)
        self.assertIn("EXECUTION_METADATA_UNREADABLE", self.codes(batch))


class TestDeduplication(JunitTestCase):
    def test_identical_testcases_collapse_and_list_sources(self):
        self.write_text(
            "fulfillment-service/it/reports/a.xml", simple_suite_xml()
        )
        self.write_text(
            "fulfillment-service/it/reports/b.xml", simple_suite_xml()
        )

        batch = self.collect_files()

        self.assertEqual(len(batch.records), 1)
        record = batch.records[0]
        self.assertEqual(record.source, "fulfillment-service/it/reports/a.xml")
        self.assertEqual(
            record.details["sources"],
            [
                "fulfillment-service/it/reports/a.xml",
                "fulfillment-service/it/reports/b.xml",
            ],
        )
        self.assertNotIn("EXECUTION_CONTRADICTION", self.codes(batch))

    def test_conflicting_outcomes_are_kept_and_flagged(self):
        self.write_text(
            "fulfillment-service/it/reports/a.xml",
            simple_suite_xml(outcome="passed"),
        )
        self.write_text(
            "fulfillment-service/it/reports/b.xml",
            simple_suite_xml(outcome="failed"),
        )

        batch = self.collect_files()

        self.assertEqual(len(batch.records), 1)
        record = batch.records[0]
        self.assertTrue(record.details["contradiction"])
        self.assertEqual(record.details["outcomes"], ["failed", "passed"])
        self.assertEqual(record.details["outcome"], "failed")
        self.assertIn("EXECUTION_CONTRADICTION", self.codes(batch))

    def test_output_is_deterministic_under_file_order_permutations(self):
        paths = []
        for index, name in enumerate(("a", "b", "c")):
            paths.append(
                str(
                    self.write_text(
                        "fulfillment-service/it/reports/{0}.xml".format(name),
                        simple_suite_xml(name="[It] DiskImage case {0}".format(index)),
                    )
                )
            )
        self.write_text("reports/bad.xml", "<testsuite>")

        forward = self.collect(explicit=paths)
        backward = self.collect(explicit=list(reversed(paths)))

        self.assertEqual(
            [record.to_dict() for record in forward.records],
            [record.to_dict() for record in backward.records],
        )
        self.assertEqual(
            [diagnostic.to_dict() for diagnostic in forward.diagnostics],
            [diagnostic.to_dict() for diagnostic in backward.diagnostics],
        )


class TestAttribution(JunitTestCase):
    def test_feature_key_matches_at_a_word_boundary(self):
        self.write_text(
            "reports/run.unit.xml",
            "<testsuite name=\"pytest\">"
            "<properties><property name=\"revision\" value=\"{0}\"/></properties>"
            "<testcase classname=\"mystery.pkg\" name=\"OSAC-2540 works\"/>"
            "</testsuite>".format(SHA),
        )

        batch = self.collect_files()

        self.assertEqual(batch.records[0].feature, "OSAC-2540")

    def test_partial_key_does_not_match(self):
        self.write_text(
            "reports/run.unit.xml",
            "<testsuite name=\"pytest\">"
            "<properties><property name=\"revision\" value=\"{0}\"/></properties>"
            "<testcase classname=\"mystery.pkg\" name=\"XOSAC-25400 works\"/>"
            "</testsuite>".format(SHA),
        )

        batch = self.collect_files()

        self.assertEqual(batch.records, ())
        self.assertEqual(self.codes(batch), ["EXECUTION_UNATTRIBUTED"])

    def test_mapping_name_matches_the_normalized_identity(self):
        self.write_text(
            "fulfillment-service/it/reports/mapped.xml",
            simple_suite_xml(name="[It] DiskImage creates an image"),
        )
        self.write_text(
            "config/mappings/OSAC-2540.yaml",
            "version: 1\nfeature: OSAC-2540\n"
            "mappings:\n"
            "  - tests:\n"
            "      - name: DiskImage API::DiskImage creates an image\n",
        )

        batch = self.collect_files()

        self.assertEqual(len(batch.records), 1)
        self.assertEqual(batch.records[0].feature, "OSAC-2540")

    def test_unattributed_testcases_are_counted_once(self):
        self.write_text(
            "reports/run.unit.xml",
            "<testsuite name=\"pytest\">"
            "<testcase classname=\"mystery.pkg\" name=\"test_one\"/>"
            "<testcase classname=\"mystery.pkg\" name=\"test_two\"/>"
            "</testsuite>",
        )

        batch = self.collect_files()

        self.assertEqual(batch.records, ())
        unattributed = [
            diagnostic
            for diagnostic in batch.diagnostics
            if diagnostic.code == "EXECUTION_UNATTRIBUTED"
        ]
        self.assertEqual(len(unattributed), 1)
        self.assertEqual(unattributed[0].severity, "info")
        self.assertIn("2 testcase(s)", unattributed[0].message)


class TestFailureIsolation(JunitTestCase):
    def test_one_bad_file_does_not_stop_the_rest(self):
        self.write_text(
            "fulfillment-service/it/reports/good.xml", simple_suite_xml()
        )
        self.write_text("fulfillment-service/it/reports/bad.xml", "\x00not xml")
        self.write_text("fulfillment-service/it/reports/evil.xml", "<!DOCTYPE x>")

        batch = self.collect_files()

        self.assertEqual(len(batch.records), 1)
        self.assertEqual(
            sorted(self.codes(batch)), ["JUNIT_REJECTED", "JUNIT_UNREADABLE"]
        )


if __name__ == "__main__":
    unittest.main()
