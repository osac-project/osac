#!/usr/bin/env python3
"""Diagnose an E2E test failure with Gemini via Vertex AI.

Reads already-redacted, already-gathered failure data (JUnit XML + cluster
logs/events from gather-osac-logs.sh) and writes a short root-cause
diagnosis to $GITHUB_STEP_SUMMARY. Never executes or reads anything from a
PR's own source checkout -- only the artifact directory this same job's
Gather artifacts step already produced.

Bounded by design: sends a fixed-size extract, not the full multi-file
dump, to keep the prompt (and cost) predictable regardless of how much a
given failure happened to log.
"""
import glob
import os
import re
import sys
import xml.etree.ElementTree as ET

MAX_FAILURES = 5
MAX_FAILURE_TEXT = 2000
MAX_NAME_LEN = 200
MAX_MESSAGE_LEN = 500
MAX_LOG_MATCHES = 60
MAX_LOG_LINE_LEN = 400
# Real junit.xml from these E2E suites is normally KBs, not MBs. A generous
# but hard ceiling against a pathological/runaway file, checked before
# ET.parse loads the whole thing into memory -- ET.parse itself has no
# built-in size limit.
MAX_JUNIT_BYTES = 20 * 1024 * 1024

ARTIFACT_DIR = os.environ.get("ARTIFACT_DIR", "")
JUNIT_PATH = os.environ.get("JUNIT_PATH", "")
GOOGLE_CLOUD_PROJECT = os.environ["GOOGLE_CLOUD_PROJECT"]
GOOGLE_CLOUD_LOCATION = os.environ["GOOGLE_CLOUD_LOCATION"]
SUMMARY_PATH = os.environ.get("GITHUB_STEP_SUMMARY", "")
# Set by callers that need the raw diagnosis text outside this job's own step
# summary -- e.g. ai-diagnostic-e2e.yml runs in a separate workflow_run job
# and feeds this into a Check Run body instead of (or as well as) a summary.
DIAGNOSIS_FILE = os.environ.get("DIAGNOSIS_FILE", "")
WORKFLOW_NAME = os.environ.get("WORKFLOW_NAME", "E2E job")
RUN_URL = os.environ.get("RUN_URL", "")

LOG_PATTERN = re.compile(r"error|traceback|panic|failed|exception", re.IGNORECASE)


def extract_junit_failures(path):
    if not path or not os.path.isfile(path):
        return "(no junit.xml found)"
    size = os.path.getsize(path)
    if size > MAX_JUNIT_BYTES:
        return f"(junit.xml is {size} bytes, over the {MAX_JUNIT_BYTES}-byte cap -- skipped rather than loading it whole)"
    try:
        tree = ET.parse(path)
    except ET.ParseError as exc:
        return f"(junit.xml present but failed to parse: {exc})"

    chunks = []
    for testcase in tree.getroot().iter("testcase"):
        for tag in ("failure", "error"):
            node = testcase.find(tag)
            if node is None:
                continue
            name = testcase.get("name", "unknown")[:MAX_NAME_LEN]
            message = (node.get("message") or "").strip()[:MAX_MESSAGE_LEN]
            text = (node.text or "").strip()[:MAX_FAILURE_TEXT]
            chunks.append(f"### {name}\n**{tag}**: {message}\n```\n{text}\n```")
            if len(chunks) >= MAX_FAILURES:
                return "\n\n".join(chunks)
    return "\n\n".join(chunks) if chunks else "(no failed/errored testcases in junit.xml)"


def extract_log_signal(artifact_dir):
    if not artifact_dir or not os.path.isdir(artifact_dir):
        return "(no artifact directory found)"

    matches = []
    # Flat, top-level text files first (events.txt, pods-describe.txt, pod-*.log)
    # -- these are what gather-osac-logs.sh writes for the main E2E namespace.
    for path in sorted(glob.glob(os.path.join(artifact_dir, "*.txt"))) + sorted(
        glob.glob(os.path.join(artifact_dir, "*.log"))
    ):
        try:
            with open(path, "r", errors="replace") as f:
                for line in f:
                    if LOG_PATTERN.search(line):
                        matches.append(f"[{os.path.basename(path)}] {line.strip()[:MAX_LOG_LINE_LEN]}")
                        if len(matches) >= MAX_LOG_MATCHES:
                            break
        except OSError:
            continue
        if len(matches) >= MAX_LOG_MATCHES:
            break

    return "\n".join(matches) if matches else "(no error/warning lines matched)"


def call_gemini(prompt):
    from google import genai

    client = genai.Client(
        vertexai=True, project=GOOGLE_CLOUD_PROJECT, location=GOOGLE_CLOUD_LOCATION
    )
    resp = client.models.generate_content(model="gemini-2.5-flash", contents=prompt)
    return resp.text or "(empty response from Gemini)"


def main():
    junit_section = extract_junit_failures(JUNIT_PATH)
    log_section = extract_log_signal(ARTIFACT_DIR)

    prompt = f"""You are diagnosing a failed GitHub Actions workflow run
named "{WORKFLOW_NAME}", part of OSAC (an OpenShift-based fulfillment
platform). This run installs OSAC components onto a real OpenShift/KubeVirt
cluster and runs a pytest E2E suite against it. The run's own logs and
job list are at: {RUN_URL or "(url unavailable)"}

Below are the only two sources of evidence you have -- do not assume any
other CI system (Jenkins, GitLab CI, Tekton, etc.) is involved, and do not
invent log locations that weren't given to you:

1. JUnit failures/errors from the pytest suite (parsed from junit.xml).
2. Lines matching error/traceback/panic/failed/exception, grepped from the
   OpenShift pod logs, `oc describe` output, and Kubernetes events that
   this run's log-gathering step collected from the target cluster (this
   is NOT the GitHub Actions job log itself). If this section says no
   artifact directory was found, that only means no cluster evidence was
   collected for this run -- it is not proof of what went wrong or when;
   rely on the GitHub Actions run's own job logs at the URL above for
   what actually happened.

Given only this evidence, write a SHORT (under 200 words) root-cause
diagnosis for a developer who has not looked at the run yet: what likely
broke, which component is implicated, and one concrete next step (point
them at the GitHub Actions run's own job logs at the URL above, not a
generic/other CI system). If the evidence is insufficient to say anything
confident, say so plainly rather than guessing.

## JUnit failures
{junit_section}

## Matching log/event lines (grepped for error/traceback/panic/failed/exception)
{log_section}
"""

    try:
        diagnosis = call_gemini(prompt)
    except Exception as exc:  # noqa: BLE001 -- must never crash the job
        diagnosis = f"_AI diagnosis unavailable: {exc}_"

    if SUMMARY_PATH:
        with open(SUMMARY_PATH, "a") as f:
            f.write(f"## AI Failure Diagnosis: {WORKFLOW_NAME}\n\n")
            f.write(diagnosis.strip() + "\n\n")
            if RUN_URL:
                f.write(f"[Full run]({RUN_URL})\n")
    if DIAGNOSIS_FILE:
        with open(DIAGNOSIS_FILE, "w") as f:
            f.write(diagnosis.strip() + "\n")
    if not SUMMARY_PATH and not DIAGNOSIS_FILE:
        print(diagnosis)


if __name__ == "__main__":
    sys.exit(main())
