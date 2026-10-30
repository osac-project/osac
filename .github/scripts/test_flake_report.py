"""Report behavior and local planner execution; no GitHub, Vault or Slack calls."""

import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from typing import Any

import yaml
from flake_report import aggregate, format_overall_failure_risk, render_markdown, render_slack

ROOT = Path(__file__).resolve().parents[2]
RUN = {
    "run_id": 123,
    "run_attempt": 1,
    "sha": "a" * 40,
    "conclusion": "success",
    "url": "https://github.com/osac-project/osac/actions/runs/123",
    "report_url": "https://github.com/osac-project/osac/actions/runs/456",
    "started_at": "2026-10-19T03:30:00Z",
}
TEST_SHA = "b" * 40
SUITES = [
    {"group": "unit", "id": "Unit tests (fulfillment-service)"},
    {"group": "unit", "id": "Unit tests (osac-ui)"},
    {"group": "integration", "id": "Integration test (fulfillment-service)"},
    {"group": "integration", "id": "Integration test (osac-operator)"},
]


def plan(runs: int = 3) -> dict[str, Any]:
    return {key: RUN[key] for key in ("run_id", "run_attempt", "sha")} | {
        "test_sha": TEST_SHA,
        "runs": runs,
        "suites": [suite.copy() for suite in SUITES],
    }


def job(suite: int = 0, iteration: int = 1, conclusion: str | None = "success") -> dict[str, Any]:
    component = SUITES[suite]["id"].partition("(")[2].removesuffix(")")
    return {
        "id": suite * 1000 + iteration,
        "run_id": RUN["run_id"],
        "run_attempt": RUN["run_attempt"],
        "head_sha": RUN["sha"],
        "name": f"{SUITES[suite]['group'].title()} repetition {iteration} / {SUITES[suite]['id']} / {component}",
        "status": "completed",
        "conclusion": conclusion,
        "steps": [],
    }


def jobs(runs: int = 3) -> list[dict[str, Any]]:
    return [job(suite, iteration) for suite in range(len(SUITES)) for iteration in range(1, runs + 1)]


def summarize(observations: list[dict[str, Any]], *, runs: int = 3, conclusion: str = "success") -> dict[str, Any]:
    return aggregate(plan(runs), [{"jobs": observations}], [], RUN | {"conclusion": conclusion}, [])


def run_cli(directory: Path, conclusion: str, *, github_output: Path | None = None) -> subprocess.CompletedProcess[str]:
    command = [sys.executable, str(ROOT / ".github/scripts/flake_report.py")]
    for name, value in {
        "plan": directory / "input/plan.json",
        "jobs": directory / "input/jobs.json",
        "artifacts": directory / "input/artifacts.json",
        "output": directory / "report",
        "run-id": RUN["run_id"],
        "attempt": RUN["run_attempt"],
        "sha": RUN["sha"],
        "conclusion": conclusion,
        "run-url": RUN["url"],
        "report-url": RUN["report_url"],
        "started-at": RUN["started_at"],
    }.items():
        command.extend((f"--{name}", str(value)))
    if github_output:
        command.extend(("--github-output", str(github_output)))
    return subprocess.run(command, capture_output=True, text=True, check=False)


class ReportTests(unittest.TestCase):
    def test_counts_and_status_for_clean_mixed_and_failed_campaigns(self) -> None:
        for failures, status in (([], "clean"), ([0, 6], "failures detected"), (list(range(12)), "failures detected")):
            with self.subTest(failures=failures):
                observations = jobs()
                for index in failures:
                    observations[index]["conclusion"] = "failure"
                report = summarize(observations, conclusion="failure" if failures else "success")
                self.assertEqual(report["status"], status)
                self.assertEqual(sum(suite["passed"] for suite in report["suites"]), 12 - len(failures))
                self.assertEqual(sum(suite["failed"] for suite in report["suites"]), len(failures))
                self.assertEqual(sum(suite["missing"] for suite in report["suites"]), 0)

    def test_component_results_ignore_the_leaf_job_display_name(self) -> None:
        observations = jobs()
        for observation, leaf in zip(
            observations[:3], ("Run", "fulfillment-service", "osac-metering/adapters"), strict=True
        ):
            observation["name"] = observation["name"].rsplit(" / ", 1)[0] + f" / {leaf}"
            observation["conclusion"] = "failure"
        report = summarize(observations, conclusion="failure")
        self.assertEqual(report["status"], "failures detected")
        self.assertEqual(
            [(suite["suite"], suite["failed"]) for suite in report["suites"] if suite["failed"]], [(SUITES[0]["id"], 3)]
        )

    def test_non_successful_job_outcomes_are_incomplete(self) -> None:
        for outcome in ("cancelled", "timed_out", "skipped", None, "in_progress"):
            with self.subTest(outcome=outcome):
                observations = jobs()
                observations[0]["conclusion"] = outcome
                if outcome == "in_progress":
                    observations[0]["status"] = "in_progress"
                report = summarize(observations)
                self.assertEqual(report["status"], "incomplete")
                self.assertEqual(sum(suite["passed"] for suite in report["suites"]), 11)
                self.assertEqual(sum(suite["other"] for suite in report["suites"]), 1)
                slack = render_slack(report)["text"]
                self.assertIn("- \U0001f7e1 fulfillment-service:", slack)
                self.assertIn("- \U0001f7e2 All other 1 suite passed", slack)

    def test_incomplete_reports_keep_warnings_and_failure_color(self) -> None:
        partial = jobs()[1:]
        partial[0]["conclusion"] = "failure"
        partial[1]["conclusion"] = "timed_out"
        for observations, conclusion, icon, warning in (
            (jobs()[1:], "success", "\U0001f7e1", "missing 1"),
            ([], "success", "\U0001f7e1", "Incomplete report"),
            (jobs(), "cancelled", "\U0001f7e1", "Incomplete report"),
            (jobs(), "failure", "\U0001f7e1", "Incomplete report"),
            (partial, "failure", "\U0001f534", "1 timed out, missing 1"),
        ):
            with self.subTest(conclusion=conclusion, warning=warning):
                report = summarize(observations, conclusion=conclusion)
                self.assertEqual(report["status"], "incomplete")
                slack = render_slack(report)["text"]
                self.assertTrue(slack.startswith(icon))
                self.assertIn(warning, slack)
                self.assertIn("Incomplete report", render_markdown(report))
                self.assertIn("*Overall failure risk: Unavailable*", slack)
                self.assertIn("**Overall failure risk: Unavailable**", render_markdown(report))
                self.assertNotIn("No failures detected", slack)
                self.assertNotIn("No failures detected", render_markdown(report))
                expected_status = (
                    "Failures detected in 1 of 4 suites — Incomplete report"
                    if any(observation["conclusion"] == "failure" for observation in observations)
                    else "Incomplete report"
                )
                self.assertIn(f"*{expected_status}*", slack)

    def test_invalid_metadata_preserves_observed_failures(self) -> None:
        observations = jobs()
        observations[0]["conclusion"] = "failure"
        for metadata in (
            None,
            plan() | {"run_attempt": 2},
            plan() | {"test_sha": "invalid"},
            plan() | {"suites": [{}]},
        ):
            with self.subTest(metadata=metadata):
                report = aggregate(metadata, [{"jobs": observations}], [], RUN | {"conclusion": "failure"}, [])
                self.assertEqual(report["status"], "incomplete")
                self.assertIsNone(report["run"]["test_sha"])
                self.assertTrue(all(suite["expected"] is None for suite in report["suites"]))
                self.assertEqual(sum(suite["passed"] for suite in report["suites"]), 11)
                self.assertEqual(sum(suite["failed"] for suite in report["suites"]), 1)
                self.assertIn(f"{RUN['url']}/job/1", render_markdown(report))
                self.assertIn("Commit: unknown", render_slack(report)["text"])
                for text in (render_slack(report)["text"], render_markdown(report)):
                    self.assertIn("Failures detected in 1 observed suite — Incomplete report", text)
                    self.assertNotIn("of 4 suites", text)

    def test_jobs_with_wrong_provenance_are_excluded(self) -> None:
        for field, value in (("run_id", 999), ("run_attempt", 2), ("head_sha", "c" * 40)):
            with self.subTest(field=field):
                observations = jobs()
                observations[0].update({field: value, "conclusion": "failure"})
                report = summarize(observations)
                self.assertEqual(report["status"], "incomplete")
                self.assertEqual(sum(suite["passed"] for suite in report["suites"]), 11)
                self.assertEqual(sum(suite["failed"] for suite in report["suites"]), 0)
                self.assertEqual(sum(suite["missing"] for suite in report["suites"]), 1)

    def test_partial_rerun_preserves_only_current_attempt_results(self) -> None:
        current = job(conclusion="failure") | {"id": 9001, "run_attempt": 2}
        report = aggregate(plan(), [{"jobs": [*jobs(), current]}], [], RUN | {"run_attempt": 2}, [])
        self.assertEqual(report["status"], "incomplete")
        failures = [execution["job_id"] for suite in report["suites"] for execution in suite["failures"]]
        self.assertEqual(failures, [9001])
        self.assertEqual(sum(suite["passed"] for suite in report["suites"]), 0)

    def test_duplicate_and_unplanned_jobs_do_not_hide_missing_repetitions(self) -> None:
        first = job()
        duplicate = first | {"id": 9002, "conclusion": "failure"}
        unplanned = job(iteration=4, conclusion="failure")
        unknown = first | {"id": 9003, "name": "Unit repetition 1 / Unknown suite / Run", "conclusion": "failure"}
        report = summarize([first, first, duplicate, unplanned, unknown, *jobs()[3:], {"name": "Plan"}])
        self.assertEqual(report["status"], "incomplete")
        suite = next(suite for suite in report["suites"] if suite["suite"] == SUITES[0]["id"])
        self.assertEqual((suite["passed"], suite["failed"], suite["missing"]), (1, 2, 2))
        failures = {execution["job_id"] for suite in report["suites"] for execution in suite["failures"]}
        self.assertEqual(failures, {9002, unplanned["id"], 9003})

    def test_unavailable_api_data_marks_the_report_incomplete(self) -> None:
        for pages, artifacts, passed in (
            (None, [], 0),
            ([{"message": "API unavailable"}], [], 0),
            ([{"jobs": jobs()}], None, 12),
        ):
            with self.subTest(pages=pages, artifacts=artifacts):
                report = aggregate(plan(), pages, artifacts, RUN, [])
                self.assertEqual(report["status"], "incomplete")
                self.assertEqual(sum(suite["passed"] for suite in report["suites"]), passed)
                self.assertEqual(sum(suite["missing"] for suite in report["suites"]), 12 - passed)
                self.assertTrue(report["issues"])

    def test_execution_links_use_only_matching_available_diagnostics(self) -> None:
        observations = jobs()
        for index in (6, 7):
            observations[index].update(conclusion="failure", steps=[{"name": "Run tests", "conclusion": "failure"}])
        artifacts = [
            {"id": 1, "name": "logs-integration-1-attempt-1", "expired": False},
            {"id": 2, "name": "logs-integration-1-attempt-1", "expired": False},
            {"id": 3, "name": "logs-integration-2-attempt-1", "expired": True},
            {"id": 4, "name": "logs-integration-2-attempt-2", "expired": False},
            {"id": 5, "name": "osac-installer-logs-integration-1-attempt-1", "expired": False},
        ]
        report = aggregate(
            plan(), [{"jobs": observations}], [{"artifacts": artifacts}], RUN | {"conclusion": "failure"}, []
        )
        self.assertEqual({artifact["id"] for artifact in report["artifacts"]}, {1, 2, 5})
        markdown = render_markdown(report)
        first = next(line for line in markdown.splitlines() if line.startswith("| IT / fulfillment-service | 1/3 |"))
        second = next(line for line in markdown.splitlines() if line.startswith("| IT / fulfillment-service | 2/3 |"))
        self.assertIn(f"[View job]({RUN['url']}/job/2001)", first)
        for artifact in (1, 2):
            self.assertIn(f"[Logs]({RUN['url']}/artifacts/{artifact})", first)
        self.assertNotIn(f"{RUN['url']}/artifacts/5", first)
        self.assertTrue(second.endswith("| — |"))
        self.assertIn(f"{RUN['url']}/artifacts/5", markdown)

    def test_large_paginated_campaign_keeps_every_result_and_artifact(self) -> None:
        observations = jobs(runs=256)
        failed = (0, 512, 1023)
        for index in failed:
            observations[index]["conclusion"] = "failure"
        artifacts = [
            {"id": iteration, "name": f"logs-integration-{iteration}-attempt-1", "expired": False}
            for iteration in range(1, 257)
        ]
        report = aggregate(
            plan(256),
            [{"jobs": observations[start : start + 100]} for start in range(0, len(observations), 100)],
            [{"artifacts": artifacts[start : start + 100]} for start in range(0, len(artifacts), 100)],
            RUN | {"conclusion": "failure"},
            [],
        )
        self.assertEqual(report["status"], "failures detected")
        self.assertEqual(sum(suite["passed"] for suite in report["suites"]), 1021)
        self.assertEqual(sum(suite["failed"] for suite in report["suites"]), 3)
        self.assertEqual(sum(suite["missing"] for suite in report["suites"]), 0)
        self.assertEqual(len(report["artifacts"]), 256)
        markdown = render_markdown(report)
        for index in failed:
            self.assertIn(f"{RUN['url']}/job/{observations[index]['id']}", markdown)
        self.assertIn(f"{RUN['url']}/artifacts/256", markdown)
        self.assertIn("*Failures detected in 3 of 4 suites*", render_slack(report)["text"])

    def test_clean_and_failing_slack_messages(self) -> None:
        links = (
            f"Commit: <https://github.com/osac-project/osac/commit/{TEST_SHA}|{TEST_SHA[:7]}> · "
            f"<{RUN['url']}|Campaign>"
        )
        for failing, title, total, integration, unit in (
            (
                False,
                "\U0001f7e2 *OSAC Weekly Test Stability* | Oct 26, 2026",
                "*No failures detected*",
                "*Integration tests* - \U0001f7e2 All 2 suites passed",
                "*Unit tests* - \U0001f7e2 All 2 suites passed",
            ),
            (
                True,
                "\U0001f534 *OSAC Weekly Test Stability* | Oct 19, 2026",
                "*Failures detected in 2 of 4 suites*",
                "*Integration tests*\n"
                "- \U0001f534 fulfillment-service: 2/3 executions failed (66.7%)\n"
                "- \U0001f7e2 All other 1 suite passed",
                "*Unit tests*\n"
                "- \U0001f534 fulfillment-service: 1/3 executions failed (33.3%)\n"
                "- \U0001f7e2 All other 1 suite passed",
            ),
        ):
            with self.subTest(failing=failing):
                observations = jobs()
                if failing:
                    for index in (0, 6, 7):
                        observations[index]["conclusion"] = "failure"
                report = summarize(observations, conclusion="failure" if failing else "success")
                if not failing:
                    report["run"]["started_at"] = "2026-10-25T23:30:00-04:00"
                payload = render_slack(report)
                risk = "77.8%" if failing else "0.0%"
                sections = (title, f"*Overall failure risk: {risk}*", total, integration, unit, links)
                self.assertEqual(payload["text"], "\n\n".join((*sections, f"<{RUN['report_url']}|View Report>")))
                self.assertEqual(list(sections), [block["text"]["text"] for block in payload["blocks"][:-1]])
                self.assertEqual(
                    payload["blocks"][-1],
                    {
                        "type": "actions",
                        "elements": [
                            {
                                "type": "button",
                                "text": {"type": "plain_text", "text": "View Report"},
                                "url": RUN["report_url"],
                            }
                        ],
                    },
                )
                self.assertNotIn("runs failed", payload["text"])
                self.assertNotIn(RUN["report_url"], links)
                self.assertFalse(payload["unfurl_links"])
                self.assertFalse(payload["unfurl_media"])

    def test_overall_failure_risk_for_complete_reports(self) -> None:
        for failures, risk in (
            ((0,) * 10, "0.0%"),
            ((2, 2, 3, 3, 2, 0, 0, 0, 0, 0), "11.4%"),
            ((100,) + (0,) * 9, "100.0%"),
        ):
            with self.subTest(failures=failures):
                metadata = plan(100)
                metadata["suites"] = [
                    {"group": "unit", "id": f"Unit tests (component-{index})"} for index in range(len(failures))
                ]
                observations = [
                    job(iteration=iteration)
                    | {
                        "id": index * 1000 + iteration,
                        "name": f"Unit repetition {iteration} / {suite['id']} / Run",
                        "conclusion": "failure" if iteration <= failures[index] else "success",
                    }
                    for index, suite in enumerate(metadata["suites"])
                    for iteration in range(1, 101)
                ]
                conclusion = "failure" if any(failures) else "success"
                report = aggregate(metadata, [{"jobs": observations}], [], RUN | {"conclusion": conclusion}, [])
                self.assertEqual(format_overall_failure_risk(report), risk)
                slack = render_slack(report)["text"]
                markdown = render_markdown(report)
                self.assertIn(f"*Overall failure risk: {risk}*", slack)
                self.assertIn(f"**Overall failure risk: {risk}**", markdown)
                self.assertNotIn("executions per suite", slack)
                self.assertIn("calculated from observed suite failure rates", markdown)
                self.assertIn("assuming independent suite outcomes", markdown)
                self.assertIn("Zero observed failures do not establish zero future risk.", markdown)
        self.assertEqual(format_overall_failure_risk({"status": "clean", "suites": []}), "Unavailable")

    def test_failure_summary_counts_suites_and_preserves_per_suite_rates(self) -> None:
        metadata = plan(100)
        metadata["suites"] = [
            {"group": group, "id": f"{prefix} ({component})"}
            for group, prefix in (("integration", "Integration test"), ("unit", "Unit tests"))
            for component in ("fulfillment-service", "osac-operator", "osac-installer", "osac-metering", "osac-ui")
        ]
        observations = [
            job(iteration=iteration)
            | {
                "id": index * 1000 + iteration,
                "name": f"{suite['group'].title()} repetition {iteration} / {suite['id']} / Run",
                "conclusion": "failure" if index == 0 and iteration <= 16 else "success",
            }
            for index, suite in enumerate(metadata["suites"])
            for iteration in range(1, 101)
        ]
        report = aggregate(metadata, [{"jobs": observations}], [], RUN | {"conclusion": "failure"}, [])
        slack = render_slack(report)["text"]
        markdown = render_markdown(report)
        for text in (slack, markdown):
            self.assertIn("Failures detected in 1 of 10 suites", text)
            self.assertNotIn("1.6%", text)
            self.assertNotIn("3.2%", text)
        self.assertIn("fulfillment-service: 16/100 executions failed (16%)", slack)
        self.assertIn("All other 4 suites passed", slack)
        self.assertIn("All 5 suites passed", slack)
        self.assertIn("| \U0001f534 fulfillment-service | 16 / 100 | **16.0%** |", markdown)
        self.assertEqual(markdown.count("| \U0001f7e2"), 9)

    def test_failure_summary_uses_singular_and_excludes_incomplete_only_suites(self) -> None:
        observations = jobs()
        observations[0]["conclusion"] = "failure"
        report = summarize(observations[:-1], conclusion="failure")
        for text in (render_slack(report)["text"], render_markdown(report)):
            self.assertIn("Failures detected in 1 of 4 suites — Incomplete report", text)
            self.assertIn("missing 1", text)
        metadata = plan() | {"suites": [SUITES[0].copy()]}
        report = aggregate(metadata, [{"jobs": observations[:3]}], [], RUN | {"conclusion": "failure"}, [])
        for text in (render_slack(report)["text"], render_markdown(report)):
            self.assertIn("Failures detected in 1 of 1 suite", text)
            self.assertNotIn("1 of 1 suites", text)

    def test_markdown_layout_and_repository_specific_links(self) -> None:
        observations = jobs()
        for index in (0, 6, 7):
            observations[index]["conclusion"] = "failure"
        url = "https://github.example.com/contributor/osac"
        report = aggregate(
            plan(), [{"jobs": observations}], [], RUN | {"url": f"{url}/actions/runs/123", "conclusion": "failure"}, []
        )
        markdown = render_markdown(report)
        self.assertTrue(
            markdown.startswith(
                "# Weekly Test Stability Report\n\n**Overall failure risk: 77.8%**\n\n"
                "\U0001f534 **Failures detected in 2 of 4 suites** | October 19, 2026"
            )
        )
        self.assertIn(f"**Commit:** [`{TEST_SHA[:7]}`]({url}/commit/{TEST_SHA})", markdown)
        self.assertIn("**Coverage:** 4 suites \u00d7 3 executions per suite", markdown)
        self.assertNotIn("| Overall |", markdown)
        self.assertNotIn("| **Integration tests** |", markdown)
        self.assertNotIn("| **Unit tests** |", markdown)
        self.assertNotIn("25.0%", markdown)
        self.assertNotIn("16.7%", markdown)
        self.assertLess(markdown.index("### Integration tests"), markdown.index("### Unit tests"))
        self.assertEqual(markdown.count("| Suite | Failed / Recorded | Failure rate |"), 2)
        self.assertIn("not individual test cases", markdown)
        self.assertIn("| \U0001f534 fulfillment-service | 1 / 3 | **33.3%** |", markdown)
        self.assertIn("| \U0001f534 fulfillment-service | 2 / 3 | **66.7%** |", markdown)
        for group, iteration, job_id in (("UT", 1, 1), ("IT", 1, 2001), ("IT", 2, 2002)):
            self.assertIn(
                f"| {group} / fulfillment-service | {iteration}/3 | "
                f"[View job]({url}/actions/runs/123/job/{job_id}) | — |",
                markdown,
            )
        self.assertIn(f"[Tested commit]({url}/commit/{TEST_SHA})", markdown)

    def test_suite_names_are_escaped_and_slack_sections_stay_bounded(self) -> None:
        for name in ("<@everyone>&a|b", "x" * 4000):
            with self.subTest(name=name[:30]):
                metadata = plan()
                metadata["suites"][0]["id"] = f"Unit tests ({name})"
                observations = jobs()
                for observation in observations[:3]:
                    observation["name"] = observation["name"].replace(SUITES[0]["id"], metadata["suites"][0]["id"])
                observations[0]["conclusion"] = "failure"
                report = aggregate(metadata, [{"jobs": observations}], [], RUN | {"conclusion": "failure"}, [])
                slack = render_slack(report)
                self.assertTrue(
                    all(len(block["text"]["text"]) <= 3000 for block in slack["blocks"] if block["type"] == "section")
                )
                if name.startswith("<"):
                    self.assertIn("&lt;@everyone&gt;&amp;a|b", slack["text"])
                    self.assertIn("&lt;@everyone&gt;&amp;a\\|b", render_markdown(report))
                    self.assertNotIn("<@everyone>", slack["text"])
                else:
                    self.assertIn("Further suite details in Report.", slack["text"])
                    self.assertIn(RUN["report_url"], slack["text"])

    def test_cli_writes_reports_and_optional_slack_output(self) -> None:
        for conclusion in ("success", "failure", "cancelled"):
            with self.subTest(conclusion=conclusion), tempfile.TemporaryDirectory() as directory:
                working = Path(directory)
                if conclusion != "cancelled":
                    inputs = working / "input"
                    inputs.mkdir()
                    metadata = plan()
                    metadata["suites"].reverse()
                    observations = jobs()
                    if conclusion == "failure":
                        for index in (0, 6):
                            observations[index].update(
                                conclusion="failure", steps=[{"name": "Run tests", "conclusion": "failure"}]
                            )
                    for name, data in {"plan": metadata, "jobs": [{"jobs": observations}], "artifacts": []}.items():
                        (inputs / f"{name}.json").write_text(json.dumps(data))
                result = run_cli(working, conclusion)
                self.assertEqual(result.returncode, int(conclusion == "cancelled"), result.stderr)
                output = working / "report"
                self.assertEqual({path.name for path in output.iterdir()}, {"summary.md", "results.json"})

                github_output = working / "github-output"
                github_output.write_text("existing=value\n")
                result = run_cli(working, conclusion, github_output=github_output)
                self.assertEqual(result.returncode, int(conclusion == "cancelled"), result.stderr)
                self.assertEqual({path.name for path in output.iterdir()}, {"summary.md", "results.json"})
                report = json.loads((output / "results.json").read_text())
                for field in ("sha", "run_id", "run_attempt"):
                    self.assertEqual(report["run"][field], RUN[field])
                self.assertEqual(report["run"]["test_sha"], None if conclusion == "cancelled" else TEST_SHA)
                self.assertEqual(
                    report["status"],
                    {"success": "clean", "failure": "failures detected", "cancelled": "incomplete"}[conclusion],
                )
                lines = github_output.read_text().splitlines()
                self.assertEqual(len(lines), 2)
                self.assertEqual(lines[0], "existing=value")
                name, payload = lines[1].split("=", 1)
                self.assertEqual(name, "slack_payload")
                self.assertEqual(json.loads(payload), render_slack(report))
                failures = [(suite["group"], failure) for suite in report["suites"] for failure in suite["failures"]]
                self.assertCountEqual(
                    [(group, failure["job_id"]) for group, failure in failures],
                    [("unit", 1), ("integration", 2001)] if conclusion == "failure" else [],
                )
                for _, failure in failures:
                    self.assertEqual(failure["iteration"], 1)
                    self.assertEqual(failure["conclusion"], "failure")
                    self.assertEqual(failure["failed_steps"], ["Run tests"])
                    self.assertEqual(failure["url"], f"{RUN['url']}/job/{failure['job_id']}")
                    self.assertIn(failure["url"], (output / "summary.md").read_text())


class WorkflowTests(unittest.TestCase):
    def test_slack_notification_requires_a_successful_report(self) -> None:
        workflow = yaml.safe_load((ROOT / ".github/workflows/flake-report.yml").read_text())
        notify = workflow["jobs"]["notify"]
        self.assertEqual(notify["needs"], "report")
        self.assertEqual(
            " ".join(notify["if"].split()),
            "always() && needs.report.result == 'success' && "
            "github.repository == 'osac-project/osac' && "
            "needs.report.outputs.slack_payload != '' && "
            "github.event.workflow_run.conclusion != 'cancelled'",
        )


@unittest.skipUnless(shutil.which("jq"), "planner tests require jq")
class PlanTests(unittest.TestCase):
    def execute_plan(
        self, runs: str, parallel: str, test_sha: str = TEST_SHA
    ) -> tuple[subprocess.CompletedProcess[str], dict[str, Any], dict[str, Any]]:
        workflow = yaml.safe_load((ROOT / ".github/workflows/flake-detection.yml").read_text())
        script = next(step["run"] for step in workflow["jobs"]["plan"]["steps"] if step.get("id") == "plan")
        with tempfile.TemporaryDirectory() as directory:
            working = Path(directory)
            yq = working / "yq"
            yq.write_text('#!/bin/sh\nprintf "%s\\n" "$CATALOG"\n')
            yq.chmod(0o755)
            output = working / "outputs"
            env = os.environ | {
                "PATH": f"{working}:{os.environ['PATH']}",
                "CATALOG": json.dumps(SUITES),
                "RUNS": runs,
                "PARALLEL": parallel,
                "TEST_SHA": test_sha,
                "GITHUB_RUN_ID": str(RUN["run_id"]),
                "GITHUB_RUN_ATTEMPT": str(RUN["run_attempt"]),
                "GITHUB_SHA": RUN["sha"],
                "GITHUB_OUTPUT": str(output),
            }
            result = subprocess.run(
                ["bash", "-c", script], cwd=working, env=env, capture_output=True, text=True, check=False
            )
            outputs = dict(line.split("=", 1) for line in output.read_text().splitlines()) if output.exists() else {}
            manifest = working / "flake-plan.json"
            metadata = json.loads(manifest.read_text()) if manifest.exists() else {}
            return result, {key: json.loads(value) for key, value in outputs.items()}, metadata

    def test_defaults_boundaries_and_selected_commit(self) -> None:
        for runs, parallel, test_sha, shares in (
            (100, 20, RUN["sha"], (10, 10)),
            (256, 3, TEST_SHA, (1, 2)),
            (1, 2, TEST_SHA, (1, 1)),
        ):
            with self.subTest(runs=runs, parallel=parallel):
                result, outputs, metadata = self.execute_plan(str(runs), str(parallel), test_sha)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(outputs["iterations"], list(range(1, runs + 1)))
                self.assertEqual((outputs["unit_parallelism"], outputs["integration_parallelism"]), shares)
                self.assertEqual(
                    (metadata["runs"], metadata["sha"], metadata["test_sha"]), (runs, RUN["sha"], test_sha)
                )
                self.assertEqual(metadata["suites"], SUITES)

    def test_invalid_inputs_are_rejected(self) -> None:
        for runs, parallel in (("0", "20"), ("257", "20"), ("1.5", "20"), ("2", "1"), ("2", "2.5")):
            with self.subTest(runs=runs, parallel=parallel):
                result, _, _ = self.execute_plan(runs, parallel)
                self.assertNotEqual(result.returncode, 0)


if __name__ == "__main__":
    unittest.main()
