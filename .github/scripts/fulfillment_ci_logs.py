"""Capture fulfillment integration output privately and publish allowlisted diagnostics."""

import functools
import json
import os
import re
import subprocess
import sys
import tempfile
from collections.abc import Sequence
from contextlib import suppress
from pathlib import Path
from typing import Any, BinaryIO

PHASES = ("service-only", "networking-operator", "bmaas-disabled", "compute-storage")
ARTIFACT_DIR = Path("it-logs")
TRANSCRIPT = ARTIFACT_DIR / "fulfillment-test.log"
EVENTS = (
    (b"make test-platform-images", "Test image build/load invoked"),
    (b"make install-osac ", "OSAC installation invoked"),
    (b"ERROR: failed to solve:", "Container image build failed"),
    (b"Error: INSTALLATION FAILED:", "Helm installation failed"),
    (b"Error: UPGRADE FAILED:", "Helm upgrade failed"),
    (b"error: timed out waiting for the condition", "Kubernetes readiness wait timed out"),
    (b"Failed to compile ", "Ginkgo compilation failed"),
    (b"A BeforeSuite node failed so all tests were skipped.", "Ginkgo suite setup failed"),
    (b"SUCCESS! -- ", "Ginkgo suite passed"),
    (b"FAIL! -- ", "Ginkgo suite failed"),
    (b"FAIL! - ", "Ginkgo suite failed"),
)
COLOR = re.compile(rb"\x1b\[[0-9;]*m")
MAKE_ERROR = re.compile(rb"make(?:\[[0-9]+\])?: \*\*\*")
FAILURE_STATES = {
    "failed": "failed",
    "timedout": "timed out",
    "panicked": "panicked",
    "aborted": "aborted",
    "interrupted": "interrupted",
}
OTHER_STATES = ("passed", "skipped", "pending")
SUITE_HOOKS = ("BeforeSuite", "AfterSuite", "SynchronizedBeforeSuite", "SynchronizedAfterSuite")
SOURCE_PATH = re.compile(r"fulfillment-service/it/[A-Za-z0-9_]+\.go")
SPEC_DECLARATION = re.compile(r'\s*(?:var _ = )?(?:It|Specify|Entry)\(\s*(["`])([^"`\\\r\n]{1,240})\1\s*,')
SUMMARY_FOOTER = "\nSee the fulfillment integration log artifact for the transcript and available diagnostics.\n"
MAX_EVENTS = 100
MAX_FAILURES = 10
READ_SIZE = 64 * 1024


def capture_command(command: Sequence[str], capture: BinaryIO) -> tuple[int, bool]:
    """Keep draining the combined stream if private capture storage fails."""
    captured = True
    with subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT) as process:
        assert process.stdout is not None
        while chunk := process.stdout.read1(READ_SIZE):
            if captured:
                try:
                    capture.write(chunk)
                except OSError:
                    captured = False
        status = process.wait()
    return (128 - status if status < 0 else status), captured


def event_label(line: bytes) -> str | None:
    # Compare bytes to known markers. Never return a slice of the input.
    line = COLOR.sub(b"", line).lstrip()
    if line.startswith(b"cd ../fulfillment-service && ") and b" ginkgo run " in line:
        for phase in PHASES:
            if b"/" + phase.encode() + b'.json"' in line:
                return f"Ginkgo phase invoked: {phase}"
    if MAKE_ERROR.match(line):
        return "Make reported a recipe failure"
    return next((label for prefix, label in EVENTS if line.startswith(prefix)), None)


def write_transcript(capture: BinaryIO, status: int) -> None:
    capture.seek(0)
    events = []
    while line := capture.readline(READ_SIZE):
        if len(line) == READ_SIZE and not line.endswith(b"\n"):
            # Skip an oversized line in bounded reads, including any apparent markers in its tail.
            while (rest := capture.readline(READ_SIZE)) and not rest.endswith(b"\n"):
                pass
            continue
        label = event_label(line)
        if label and (not events or events[-1] != label):
            events.append(label)
            if len(events) == MAX_EVENTS:
                events.append("Event limit reached")
                break
    if not events:
        events.append("No recognized events")
    ARTIFACT_DIR.mkdir(parents=True, exist_ok=True)
    lines = ["Fulfillment integration test transcript (recognized events only)", *events, f"Make exit status: {status}"]
    with tempfile.TemporaryDirectory(prefix=".fulfillment-transcript-", dir=ARTIFACT_DIR) as staging:
        staged = Path(staging) / TRANSCRIPT.name
        staged.write_text("\n".join(lines) + "\n", encoding="utf-8")
        staged.replace(TRANSCRIPT)


def phase_failures(path: Path) -> tuple[list[dict[str, Any]], bool]:
    """Return usable failures and whether all report records were readable."""
    try:
        reports = json.loads(path.read_bytes())
    except (OSError, ValueError, RecursionError):
        return [], False
    if not isinstance(reports, list) or not reports:
        return [], False
    failures = []
    complete = True
    for report in reports:
        specs = report.get("SpecReports") if isinstance(report, dict) else None
        if not isinstance(specs, list) or not specs:
            complete = False
            continue
        for spec in specs:
            state = spec.get("State") if isinstance(spec, dict) else None
            if isinstance(state, str) and state in FAILURE_STATES:
                failures.append(spec)
                if not isinstance(spec.get("LeafNodeLocation"), dict) or not isinstance(spec.get("Failure"), dict):
                    complete = False
            elif state not in OTHER_STATES:
                complete = False
    return failures, complete


@functools.cache
def source_lines(path: str) -> list[str]:
    # Read committed source, even if the test wrote files into the checkout.
    result = subprocess.run(
        ["git", "show", f"HEAD:{path}"], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, check=False
    )
    return result.stdout.decode("utf-8").splitlines() if result.returncode == 0 else []


def source_position(location: object) -> tuple[str | None, str]:
    if not isinstance(location, dict):
        return None, ""
    filename, line = location.get("FileName"), location.get("LineNumber")
    if not isinstance(filename, str) or type(line) is not int or line < 1:
        return None, ""
    path = filename.removeprefix(f"{Path.cwd()}/").removeprefix("github.com/osac-project/osac/")
    if not SOURCE_PATH.fullmatch(path):
        return None, ""
    lines = source_lines(path)
    if line > len(lines):
        return None, ""
    return f"{path}:{line}", "\n".join(lines[line - 1 : line + 1])


def describe_failure(phase: str, spec: dict[str, Any]) -> str:
    declaration, source = source_position(spec.get("LeafNodeLocation"))
    match = SPEC_DECLARATION.match(source)
    hook = next((hook for hook in SUITE_HOOKS if spec.get("LeafNodeType") == hook), None)
    name = f"`{match[2]}`" if match else hook or "Spec name unavailable"
    failure = spec.get("Failure")
    actual, _ = source_position(failure.get("Location") if isinstance(failure, dict) else None)
    where = ""
    if actual:
        where = f" at `{actual}`"
    elif declaration:
        where = f" (declared at `{declaration}`)"
    return f"- `{phase}`: {name} — {FAILURE_STATES[spec['State']]}{where}"


def write_failure_summary(status: int | None) -> None:
    result = "Make exit status unavailable" if status is None else f"Make exit {status}"
    lines = [f"## Fulfillment integration tests failed ({result})", ""]
    report_dir = Path(os.environ["FULFILLMENT_IT_REPORT_DIR"])
    unavailable = []
    failures = []
    for phase in PHASES:
        specs, complete = phase_failures(report_dir / f"{phase}.json")
        failures.extend((phase, spec) for spec in specs)
        if not complete:
            unavailable.append(phase)
    lines.extend(describe_failure(phase, spec) for phase, spec in failures[:MAX_FAILURES])
    if len(failures) > MAX_FAILURES:
        lines.append("- Additional failures are recorded in the Ginkgo reports.")
    if not failures:
        lines.append("No failed specs were identified in the available Ginkgo reports.")
    if unavailable:
        lines.extend(("", "Missing, malformed or incomplete Ginkgo reports: " + ", ".join(unavailable) + "."))
    with Path(os.environ["GITHUB_STEP_SUMMARY"]).open("a", encoding="utf-8") as summary:
        summary.write("\n".join(lines) + "\n" + SUMMARY_FOOTER)


def main(command: Sequence[str]) -> int:
    status = None
    transcript_ok = True
    try:
        runner_temp = Path(os.environ["RUNNER_TEMP"]).resolve()
        if runner_temp.is_relative_to(Path.cwd()) or runner_temp.is_relative_to(ARTIFACT_DIR.resolve()):
            raise ValueError("runner temp overlaps the checkout or artifact directory")
        with tempfile.TemporaryFile(dir=runner_temp) as capture:
            status, captured = capture_command(command, capture)
            if not captured:
                raise OSError("private capture failed")
            write_transcript(capture, status)
    except (KeyError, OSError, ValueError):
        transcript_ok = False
        print("::error::Could not capture or write the sanitized fulfillment transcript.", file=sys.stderr)
        with suppress(OSError):
            TRANSCRIPT.unlink(missing_ok=True)
    if status != 0:
        try:
            write_failure_summary(status)
        except (KeyError, OSError, ValueError):
            print("::warning::Fulfillment failure summary unavailable. See the log artifact.", file=sys.stderr)
    return status or int(not transcript_ok)


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
