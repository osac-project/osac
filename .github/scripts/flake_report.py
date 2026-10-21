"""Summarize repeated-workflow job results and link their diagnostic artifacts."""

import argparse
import html
import json
import re
from collections import Counter
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

JOB_NAME = re.compile(r"^(Unit|Integration) repetition ([1-9][0-9]*) / (.+) / .+$")
GROUPS = (("integration", "Integration tests"), ("unit", "Unit tests"))
SLACK_SECTION_LIMIT = 2800
FAILED_ICON = "\U0001f534"  # Red circle.
INCOMPLETE_ICON = "\U0001f7e1"  # Yellow circle.
PASSED_ICON = "\U0001f7e2"  # Green circle.
# Diagnostic artifact prefixes are produced by workflows/tests.yml.
DIAGNOSTIC_PREFIXES = {
    ("integration", "Integration test (fulfillment-service)"): "logs",
    ("integration", "Integration test (osac-installer)"): "osac-installer-logs",
}


def is_valid_plan(plan: object, run: dict[str, Any]) -> bool:
    """Check the expected suites and counts against the completed campaign."""
    if not (
        isinstance(plan, dict)
        and all(plan.get(key) == run[key] for key in ("run_id", "run_attempt", "sha"))
        and isinstance(plan.get("test_sha"), str)
        and re.fullmatch(r"[0-9a-f]{40}", plan["test_sha"]) is not None
        and type(plan.get("runs")) is int
        and 1 <= plan["runs"] <= 256
        and isinstance(plan.get("suites"), list)
        and bool(plan["suites"])
    ):
        return False

    seen = set()
    for suite in plan["suites"]:
        if (
            not isinstance(suite, dict)
            or suite.get("group") not in ("unit", "integration")
            or not isinstance(suite.get("id"), str)
            or not suite["id"]
        ):
            return False
        key = (suite["group"], suite["id"])
        if key in seen:
            return False
        seen.add(key)
    return True


def api_records(pages: object, field: str, issues: list[str]) -> list[dict[str, Any]]:
    """Combine all API pages, recording an issue if the response is unavailable."""
    if not isinstance(pages, list) or any(
        not isinstance(page, dict) or not isinstance(page.get(field), list) for page in pages
    ):
        issues.append(f"The {field.title()} API response is missing or invalid.")
        return []
    return [record for page in pages for record in page[field]]


def collect_jobs(
    jobs: list[dict[str, Any]], run: dict[str, Any], expected: dict[tuple[str, str], int], issues: list[str]
) -> dict[tuple[str, str], list[dict[str, Any]]]:
    """Group this attempt's component jobs by suite, preserving unexpected results."""
    suites = {key: [] for key in expected}
    seen = set()
    for job in jobs:
        match = JOB_NAME.fullmatch(job.get("name", ""))
        if not match or job["id"] in seen:
            continue
        seen.add(job["id"])
        if (
            any(job.get(field) != run[field] for field in ("run_id", "run_attempt"))
            or job.get("head_sha") != run["sha"]
        ):
            issues.append(f"Job {job['id']} belongs to another SHA/run attempt.")
            continue

        group, iteration, name = match.groups()
        key = (group.lower(), name)
        iteration = int(iteration)
        if expected and (key not in expected or iteration > expected[key]):
            issues.append(f"Unexpected test job: {job['name']}")
        observations = suites.setdefault(key, [])
        if any(item["iteration"] == iteration for item in observations):
            issues.append(f"Multiple jobs for the same repetition: {job['name']}")
        conclusion = job["status"]
        if conclusion == "completed":
            conclusion = job.get("conclusion") or "unknown"
        observations.append(
            {
                "iteration": iteration,
                "job_id": job["id"],
                "conclusion": conclusion,
                "failed_steps": [
                    step["name"] for step in job.get("steps", []) if step.get("conclusion") in ("failure", "timed_out")
                ],
                "url": f"{run['url']}/job/{job['id']}",
            }
        )
    return suites


def summarize_suite(group: str, name: str, jobs: list[dict[str, Any]], expected: int | None) -> dict[str, Any]:
    counts = Counter(job["conclusion"] for job in jobs)
    passed, failed = counts["success"], counts["failure"]
    other = len(jobs) - passed - failed
    missing = None
    if expected is not None:
        observed_iterations = {job["iteration"] for job in jobs if job["iteration"] <= expected}
        missing = expected - len(observed_iterations)
    return {
        "group": group,
        "suite": name,
        "expected": expected,
        "passed": passed,
        "failed": failed,
        "other": other,
        "incomplete": other + missing if missing is not None else None,
        "missing": missing,
        "outcomes": dict(counts),
        "failures": [job for job in jobs if job["conclusion"] != "success"],
    }


def collect_artifacts(records: list[dict[str, Any]], run: dict[str, Any]) -> list[dict[str, Any]]:
    """Keep available diagnostic archives from the reported attempt."""
    artifacts = {}
    for artifact in records:
        if artifact["expired"] or not artifact["name"].endswith(f"-attempt-{run['run_attempt']}"):
            continue
        artifacts[artifact["id"]] = {
            "id": artifact["id"],
            "name": artifact["name"],
            "url": f"{run['url']}/artifacts/{artifact['id']}",
        }
    return sorted(artifacts.values(), key=lambda artifact: artifact["name"])


def aggregate(
    plan: object, pages: object, artifact_pages: object, run: dict[str, Any], issues: list[str]
) -> dict[str, Any]:
    """Build the report data from the plan, job results and artifact list."""
    if not is_valid_plan(plan, run):
        issues.append("Campaign metadata is unavailable or invalid; expected counts and the tested commit are unknown.")
        plan = None
    expected = {(suite["group"], suite["id"]): plan["runs"] for suite in plan["suites"]} if plan else {}
    run = run | {"test_sha": plan["test_sha"] if plan else None}
    jobs = collect_jobs(api_records(pages, "jobs", issues), run, expected, issues)
    suites = [
        summarize_suite(group, name, observations, expected.get((group, name)))
        for (group, name), observations in jobs.items()
    ]
    artifacts = collect_artifacts(api_records(artifact_pages, "artifacts", issues), run)

    if run["conclusion"] not in ("success", "failure"):
        issues.append(f"Campaign concluded: {run['conclusion']}.")
    elif (
        expected
        and run["conclusion"] == "failure"
        and not any(suite["failed"] or suite["incomplete"] for suite in suites)
    ):
        issues.append("Campaign failed although every expected component job passed.")
    if issues or any(suite["other"] or suite["missing"] for suite in suites):
        status = "incomplete"
    elif any(suite["failed"] for suite in suites):
        status = "failures detected"
    else:
        status = "clean"
    return {"run": run, "status": status, "issues": issues, "suites": suites, "artifacts": artifacts}


def status_icon(failed: bool, incomplete: bool) -> str:
    if failed:
        return FAILED_ICON
    if incomplete:
        return INCOMPLETE_ICON
    return PASSED_ICON


def result_summary(rows: list[dict[str, Any]]) -> dict[str, Any]:
    """Combine execution counts and completeness without display formatting."""
    outcomes = Counter()
    for row in rows:
        outcomes.update(row["outcomes"])
    executions = sum(outcomes.values())
    missing = None
    if rows and all(row["missing"] is not None for row in rows):
        missing = sum(row["missing"] for row in rows)
    other_outcomes = {outcome: count for outcome, count in outcomes.items() if outcome not in ("success", "failure")}
    return {
        "executions": executions,
        "failed": outcomes["failure"],
        "affected": sum(bool(row["failed"] or row["other"] or row["missing"]) for row in rows),
        "missing": missing,
        "other_outcomes": other_outcomes,
        "complete": bool(executions) and missing == 0 and not other_outcomes,
    }


def format_failure_rate(summary: dict[str, Any], *, compact: bool = False) -> str:
    if not summary["executions"]:
        return "unknown"
    if not summary["failed"]:
        return "0%"
    percentage = summary["failed"] / summary["executions"] * 100
    return f"{round(percentage, 1):g}%" if compact else f"{percentage:.1f}%"


def format_result_notes(summary: dict[str, Any]) -> str:
    notes = [f"{count} {outcome.replace('_', ' ')}" for outcome, count in sorted(summary["other_outcomes"].items())]
    missing = summary["missing"]
    if missing != 0:
        notes.append(f"missing {missing if missing is not None else 'unknown'}")
    return ", ".join(notes)


def campaign_date(run: dict[str, Any], *, full_month: bool = False) -> str:
    """Use the campaign's start date in UTC, including when regenerating a report."""
    started = datetime.fromisoformat(run["started_at"]).astimezone(UTC)
    month = started.strftime("%B" if full_month else "%b")
    return f"{month} {started.day}, {started.year}"


def commit_link(run: dict[str, Any], *, slack: bool = False, label: str | None = None) -> str:
    sha = run["test_sha"]
    if not sha:
        return "unknown"
    repository_url = run["url"].split("/actions/runs/", 1)[0]
    url = f"{repository_url}/commit/{sha}"
    label = label or (sha[:7] if slack else f"`{sha[:7]}`")
    return f"<{url}|{label}>" if slack else f"[{label}]({url})"


def report_header(report: dict[str, Any]) -> str:
    failed = any(suite["failed"] for suite in report["suites"])
    icon = status_icon(failed, report["status"] == "incomplete")
    return f"{icon} *OSAC Weekly Test Stability* | {campaign_date(report['run'])}"


def grouped_suites(report: dict[str, Any]) -> list[tuple[str, list[dict[str, Any]]]]:
    groups = []
    for group, title in GROUPS:
        suites = [suite for suite in report["suites"] if suite["group"] == group]
        groups.append((title, suites))
    return groups


def suite_name(suite: dict[str, Any], *, slack: bool = False) -> str:
    match = re.fullmatch(r"(?:Unit tests|Integration test) \((.*)\)", suite["suite"])
    name = html.escape(match.group(1) if match else suite["suite"]).replace("\n", " ")
    return name if slack else name.replace("|", "\\|")


def suite_logs(suite: dict[str, Any], report: dict[str, Any]) -> dict[int, list[dict[str, Any]]]:
    """Match saved log archives to a suite, indexed by repetition number."""
    prefix = DIAGNOSTIC_PREFIXES.get((suite["group"], suite["suite"]))
    if not prefix:
        return {}
    logs = {}
    for artifact in report["artifacts"]:
        match = re.fullmatch(
            rf"{re.escape(prefix)}-{suite['group']}-([1-9][0-9]*)-attempt-{report['run']['run_attempt']}",
            artifact["name"],
        )
        if match:
            logs.setdefault(int(match.group(1)), []).append(artifact)
    return logs


def coverage_text(report: dict[str, Any], summary: dict[str, Any]) -> str:
    suites = report["suites"]
    expected = {suite["expected"] for suite in suites}
    if report["status"] != "incomplete" and len(expected) == 1:
        repetitions = next(iter(expected))
        return f"{len(suites)} suites \u00d7 {repetitions} executions = {summary['executions']:,} executions"
    return f"{len(suites)} suites · {summary['executions']:,} recorded executions"


def render_suite_table(report: dict[str, Any]) -> list[str]:
    lines = ["## Suite results", "", "| Suite | Failed / Total | Failure rate |", "|---|---:|---:|"]
    for title, suites in grouped_suites(report):
        summary = result_summary(suites)
        notes = format_result_notes(summary)
        extra = f" · {notes}" if notes else ""
        lines.append(
            f"| **{title}** | **{summary['failed']:,} / {summary['executions']:,}** | "
            f"**{format_failure_rate(summary)}**{extra} |"
        )
        for suite in suites:
            summary = result_summary([suite])
            icon = status_icon(bool(summary["failed"]), not summary["complete"])
            rate = format_failure_rate(summary)
            if suite["failed"]:
                rate = f"**{rate}**"
            notes = format_result_notes(summary)
            extra = f" · {notes}" if notes else ""
            lines.append(
                f"| {icon} {suite_name(suite)} | {suite['failed']:,} / {summary['executions']:,} | {rate}{extra} |"
            )
    return lines


def render_execution_tables(report: dict[str, Any]) -> list[str]:
    failed_rows = []
    incomplete_rows = []
    failed_suites = set()
    for _, suites in grouped_suites(report):
        for suite in suites:
            logs = suite_logs(suite, report)
            group = "IT" if suite["group"] == "integration" else "UT"
            expected = suite["expected"] if suite["expected"] is not None else "unknown"
            for execution in suite["failures"]:
                artifacts = " · ".join(
                    f"[Logs]({artifact['url']})" for artifact in logs.get(execution["iteration"], [])
                )
                job = f"[View job]({execution['url']})"
                failed = execution["conclusion"] == "failure"
                if not failed:
                    job += f" · {execution['conclusion'].replace('_', ' ')}"
                row = (
                    f"| {group} / {suite_name(suite)} | {execution['iteration']}/{expected} | "
                    f"{job} | {artifacts or '—'} |"
                )
                if failed:
                    failed_rows.append(row)
                    failed_suites.add((suite["group"], suite["suite"]))
                else:
                    incomplete_rows.append(row)
    lines = ["## Failed executions", ""]
    if failed_rows:
        executions = "execution" if len(failed_rows) == 1 else "executions"
        suites = "suite" if len(failed_suites) == 1 else "suites"
        lines.extend(
            [
                f"{len(failed_rows):,} failed {executions} across {len(failed_suites)} {suites}. "
                "Each job and artifact link points to the corresponding execution.",
                "",
                "| Suite | Execution | Failed job | Artifacts |",
                "|---|---:|---|---|",
                *failed_rows,
            ]
        )
    else:
        lines.append("No failed executions recorded.")
    if incomplete_rows:
        lines.extend(
            [
                "",
                "## Incomplete executions",
                "",
                "| Suite | Execution | Job and outcome | Artifacts |",
                "|---|---:|---|---|",
                *incomplete_rows,
            ]
        )
    return lines


def render_markdown(report: dict[str, Any]) -> str:
    run = report["run"]
    overall = result_summary(report["suites"])
    integration, unit = [result_summary(suites) for _, suites in grouped_suites(report)]
    icon = status_icon(bool(overall["failed"]), report["status"] == "incomplete")
    status = {"clean": "All suites passed", "failures detected": "Failures detected", "incomplete": "Incomplete report"}
    lines = [
        "# Weekly Test Stability Report",
        "",
        f"{icon} **{status[report['status']]}** | {campaign_date(run, full_month=True)}",
        "",
        f"**Commit:** {commit_link(run)}  ",
        f"**Campaign:** [View workflow execution]({run['url']})  ",
        f"**Coverage:** {coverage_text(report, overall)}",
        "",
        "| Overall | Integration | Unit | Affected suites |",
        "|---|---|---|---|",
        f"| {icon} **{format_failure_rate(overall)}** ({overall['failed']:,}/{overall['executions']:,}) | "
        f"**{format_failure_rate(integration)}** ({integration['failed']:,}/{integration['executions']:,}) | "
        f"**{format_failure_rate(unit)}** ({unit['failed']:,}/{unit['executions']:,}) | "
        f"**{overall['affected']}/{len(report['suites'])}** |",
        "",
        *render_suite_table(report),
        "",
        *render_execution_tables(report),
    ]
    if report["issues"]:
        lines.extend(["", f"## {INCOMPLETE_ICON} Reporting notes", *[f"- {issue}" for issue in report["issues"]]])
    if report["artifacts"]:
        lines.extend(["", "<details>", f"<summary>Diagnostic artifacts ({len(report['artifacts'])})</summary>", ""])
        lines.extend(f"- [{artifact['name']}]({artifact['url']})" for artifact in report["artifacts"])
        lines.extend(["", "</details>"])
    lines.extend(["", "---", "", f"**[View campaign]({run['url']}) · {commit_link(run, label='Tested commit')}**"])
    return "\n".join(lines) + "\n"


def slack_group_summary(title: str, suites: list[dict[str, Any]]) -> str:
    summary = result_summary(suites)
    if summary["complete"] and not summary["failed"]:
        noun = "suite" if len(suites) == 1 else "suites"
        return f"*{title}* - {PASSED_ICON} All {len(suites)} {noun} passed (0/{summary['executions']:,})"

    noun = "failure" if summary["failed"] == 1 else "failures"
    lines = [f"*{title}* - {summary['failed']:,} {noun}"]
    clean_count = 0
    clean_executions = 0
    for suite in suites:
        result = result_summary([suite])
        if result["complete"] and not result["failed"]:
            clean_count += 1
            clean_executions += result["executions"]
            continue
        icon = status_icon(bool(result["failed"]), not result["complete"])
        rate = format_failure_rate(result, compact=True)
        notes = format_result_notes(result)
        extra = f" · {notes}" if notes else ""
        lines.append(
            f"- {icon} {suite_name(suite, slack=True)}: {result['failed']:,}/{result['executions']:,} ({rate}){extra}"
        )
    if clean_count:
        noun = "suite" if clean_count == 1 else "suites"
        lines.append(f"- {PASSED_ICON} Other {clean_count} {noun}: 0/{clean_executions:,}")

    # Keep whole lines within Slack's section limit; full suite details stay in the report.
    bounded = []
    notice = "Further suite details in Report."
    for line in lines:
        if len("\n".join([*bounded, line, notice])) > SLACK_SECTION_LIMIT:
            bounded.append(notice)
            break
        bounded.append(line)
    return "\n".join(bounded)


def slack_summary_sections(report: dict[str, Any]) -> list[str]:
    overall = result_summary(report["suites"])
    rate = format_failure_rate(overall, compact=True) if overall["executions"] else "rate unknown"
    total = f"*{overall['failed']:,}/{overall['executions']:,} runs failed ({rate})*"
    expected = {suite["expected"] for suite in report["suites"]}
    repetitions = "runs per suite unknown"
    if len(expected) == 1 and None not in expected:
        qualifier = "planned " if report["status"] == "incomplete" else ""
        repetitions = f"{next(iter(expected))} {qualifier}runs per suite"
    total += f" | {repetitions}"
    if report["status"] == "incomplete":
        warning = "Incomplete report"
        notes = format_result_notes(overall)
        if notes:
            warning += f": {notes}"
        total += f"\n{INCOMPLETE_ICON} {warning}"
    groups = [slack_group_summary(title, suites) for title, suites in grouped_suites(report)]
    run = report["run"]
    links = f"Commit: {commit_link(run, slack=True)} · <{run['url']}|Campaign> · <{run['report_url']}|Report>"
    return [total, *groups, links]


def render_slack(report: dict[str, Any]) -> dict[str, Any]:
    """Show failing suites by group and combine the remaining clean suites."""
    sections = [report_header(report), *slack_summary_sections(report)]
    return {
        "text": "\n\n".join(sections),
        "unfurl_links": False,
        "unfurl_media": False,
        "blocks": [{"type": "section", "text": {"type": "mrkdwn", "text": section}} for section in sections],
    }


def write_report(report: dict[str, Any], output: Path) -> None:
    """Write the Markdown summary and complete report data."""
    output.mkdir(parents=True, exist_ok=True)
    (output / "summary.md").write_text(render_markdown(report))
    (output / "results.json").write_text(json.dumps(report, indent=2) + "\n")


def read_json(path: Path, issues: list[str]) -> object:
    try:
        return json.loads(path.read_text())
    except (OSError, ValueError) as error:
        issues.append(f"Cannot read {path.name}: {error}")
        return None


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--plan", type=Path, required=True)
    parser.add_argument("--jobs", type=Path, required=True)
    parser.add_argument("--artifacts", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--github-output", type=Path)
    parser.add_argument("--run-id", type=int, required=True)
    parser.add_argument("--attempt", type=int, required=True)
    parser.add_argument("--sha", required=True)
    parser.add_argument("--conclusion", required=True)
    parser.add_argument("--run-url", required=True)
    parser.add_argument("--report-url", required=True)
    parser.add_argument("--started-at", required=True)
    args = parser.parse_args()
    issues = []
    plan = read_json(args.plan, issues)
    jobs = read_json(args.jobs, issues)
    artifacts = read_json(args.artifacts, issues)
    run = {
        "run_id": args.run_id,
        "run_attempt": args.attempt,
        "sha": args.sha,
        "conclusion": args.conclusion,
        "url": args.run_url,
        "report_url": args.report_url,
        "started_at": args.started_at,
    }
    report = aggregate(plan, jobs, artifacts, run, issues)
    write_report(report, args.output)
    if args.github_output:
        with args.github_output.open("a") as stream:
            stream.write(f"slack_payload={json.dumps(render_slack(report))}\n")
    return int(report["status"] == "incomplete")


if __name__ == "__main__":
    raise SystemExit(main())
