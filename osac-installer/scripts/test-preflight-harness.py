#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["jsonschema==4.23.0", "pyyaml==6.0.2"]
# ///
"""Unit regressions for bounded readiness, independent cleanup and output-safe diagnostics.

Failure logs retain safe types, harness locations and allowlisted check outcomes, not raw output.

Run: uv run --script scripts/test-preflight-harness.py
"""

import importlib.util
import io
import json
import pathlib
import subprocess
import unittest
from contextlib import redirect_stdout
from functools import partial
from unittest.mock import Mock, patch

SCRIPT = pathlib.Path(__file__).with_name("test-preflight-kind.py")
spec = importlib.util.spec_from_file_location("preflight_kind", SCRIPT)
preflight = importlib.util.module_from_spec(spec)
spec.loader.exec_module(preflight)
READY = {"status": {"conditions": [{"type": "Established", "status": "True"}]}}
FAULTS = ("default-storageclass", "nodes-schedulable", "controller-credentials", "cert-manager-crds")
SENTINEL = "fake-sensitive-payload"


def report_fixture():
    """Build a schema-valid report with deliberately unsafe non-summary fields."""
    entry = dict(
        id="cert-manager-crds",
        title=SENTINEL,
        category="operators",
        severity="required",
        requiredFor=["core"],
        status="fail",
        message=SENTINEL,
        remediation=SENTINEL,
        docsURL=SENTINEL,
        observed={"value": SENTINEL},
        skipReason="",
        blocking=True,
    )
    return dict(
        apiVersion="preflight.osac.openshift.io/v1alpha1",
        kind="PreflightReport",
        phase="pre-install",
        tier="render",
        mode="enforce",
        generatedAt="2026-01-01T00:00:00Z",
        helmVersion=SENTINEL,
        kubeVersion=None,
        chart=dict(name=SENTINEL, version="1"),
        release=dict(name=SENTINEL, namespace=SENTINEL, revision=1, action="install"),
        summary=dict.fromkeys(["pass", "fail", "warn", "unknown", "skipped", "blocking"], 0),
        results=[entry],
    )


class CrdReadinessTests(unittest.TestCase):
    def setUp(self):
        self.cluster = object.__new__(preflight.Cluster)
        self.cluster.context = "kind-test"
        self.now = 0.0
        self.enterContext(patch.object(preflight.time, "monotonic", side_effect=lambda: self.now))
        self.sleep = self.enterContext(patch.object(preflight.time, "sleep", side_effect=self.advance))
        self.run = self.enterContext(patch.object(preflight.subprocess, "run"))

    def advance(self, seconds):
        self.now += seconds

    def test_pending_then_established(self):
        """Absent conditions and non-ready statuses retry until Established=True."""
        pending = [
            {},
            {"status": None},
            {"status": {}},
            {"status": {"conditions": None}},
            {"status": {"conditions": []}},
        ]
        pending += [
            {"status": {"conditions": [{"type": "Established", "status": status}]}} for status in ("False", "Unknown")
        ]
        pending += [{"status": {"conditions": [{"type": "NamesAccepted", "status": "True"}]}}]
        self.run.side_effect = [subprocess.CompletedProcess([], 0, json.dumps(doc), "") for doc in [*pending, READY]]
        self.cluster.wait_for_crd("test.example.com")
        self.assertEqual(self.run.call_count, len(pending) + 1)
        self.assertEqual(self.sleep.call_count, len(pending))
        for index, call in enumerate(self.run.call_args_list):
            self.assertEqual(call.kwargs["timeout"], 60 - index)
            self.assertEqual(
                call.args[0], ("kubectl", "--context", "kind-test", "get", "crd", "test.example.com", "-o", "json")
            )

    def test_errors_are_not_retried(self):
        """API, parser and process errors propagate unchanged without another request."""
        timeout = subprocess.TimeoutExpired("kubectl", 60, output="partial stdout", stderr="partial stderr")
        outcomes = [
            (subprocess.CompletedProcess([], 1, "api stdout", message), AssertionError)
            for message in ("Forbidden", "NotFound", "connection refused")
        ]
        outcomes += [
            (subprocess.CompletedProcess([], 0, "not JSON", ""), json.JSONDecodeError),
            (timeout, subprocess.TimeoutExpired),
            (FileNotFoundError("kubectl"), FileNotFoundError),
        ]
        for outcome, error_type in outcomes:
            with self.subTest(outcome=outcome):
                self.run.reset_mock()
                self.run.side_effect = [outcome]
                with self.assertRaises(error_type) as caught:
                    self.cluster.wait_for_crd("test.example.com")
                if isinstance(outcome, Exception):
                    self.assertIs(caught.exception, outcome)
                elif outcome.returncode:
                    self.assertIn(outcome.stdout, str(caught.exception))
                    self.assertIn(outcome.stderr, str(caught.exception))
                self.run.assert_called_once()
                self.sleep.assert_not_called()

    def test_deadline_bounds_requests_sleep_and_late_success(self):
        """Slow fractional commands share one budget, cap sleeps and reject late readiness."""
        for duration, late_success in ((10.25, False), (60, True), (59.75, False)):
            with self.subTest(duration=duration, late_success=late_success):
                self.now = 0
                self.run.reset_mock()

                def response(*args, **kwargs):
                    self.assertEqual(kwargs["timeout"], 60 - self.now)
                    self.advance(min(duration, kwargs["timeout"]))
                    doc = (
                        READY
                        if late_success
                        else {"status": {"conditions": [{"type": "Established", "status": "False"}]}}
                    )
                    return subprocess.CompletedProcess([], 0, json.dumps(doc), "")

                self.run.side_effect = response
                with self.assertRaisesRegex(AssertionError, "timed out.*test.example.com.*60s"):
                    self.cluster.wait_for_crd("test.example.com")
                self.assertEqual(self.now, 60)
                self.assertEqual(
                    [call.kwargs["timeout"] for call in self.run.call_args_list],
                    [60, 48.75, 37.5, 26.25, 15, 3.75] if duration == 10.25 else [60],
                )


class RestorationTests(unittest.TestCase):
    def test_every_restoration_is_attempted_and_failures_survive(self):
        """Each failed operation leaves later attempts intact and retains the original error."""
        failures = [(), *((label,) for label in preflight.RESTORATION_LABELS), preflight.RESTORATION_LABELS]
        for failed_labels in failures:
            for original_failure in (None, ValueError, AssertionError):
                with self.subTest(failed_labels=failed_labels, original_failure=original_failure):
                    self.check_restoration(failed_labels, original_failure)

    def check_restoration(self, failed_labels, original_failure):
        """Exercise the actual fault case with fixed, individually injectable restoration actions."""
        attempted = []
        errors = {label: OSError(SENTINEL) for label in failed_labels}

        def restore(label):
            attempted.append(label)
            if label in errors:
                raise errors[label]

        cluster = Mock(spec=preflight.Cluster, ns="test")
        cluster.kubectl.side_effect = lambda *args, **kwargs: (
            restore("uncordon") if args[0] == "uncordon" else subprocess.CompletedProcess([], 0, "node", "")
        )
        cluster.install.return_value = subprocess.CompletedProcess(
            [],
            1,
            SENTINEL,
            "Nothing was changed; oc patch storageclass; Uncordon a node; "
            "Install the cert-manager Operator; Create the controller credential Secret",
        )
        cluster.releases.return_value = ["unexpected release"] if original_failure is AssertionError else []
        cluster.report_configmap.return_value = None
        cluster.apply.side_effect = lambda manifest: restore("CRD apply")
        cluster.wait_for_crd.side_effect = lambda name: restore("CRD readiness")
        original = ValueError(SENTINEL) if original_failure is ValueError else None
        issuer = dict(id="cert-issuer-ready", status="unknown", dependsOn=["cert-manager-crds"])
        report = {"results": [dict(id=name, blocking=True) for name in FAULTS] + [dict(issuer, blocking=False)]}
        output = io.StringIO()
        caught = None
        with (
            patch.multiple(
                preflight,
                default_storageclass=Mock(return_value="standard"),
                set_default_storageclass=Mock(
                    side_effect=lambda cluster, sc, value: restore("StorageClass reset") if value else None
                ),
                create_namespace_prerequisites=Mock(side_effect=lambda cluster: restore("prerequisite creation")),
                failure_report=Mock(side_effect=original, return_value=report),
                helm_diagnostics=Mock(return_value="Helm exit code: 1; preflight report unavailable"),
            ),
            redirect_stdout(output),
        ):
            try:
                preflight.case_fresh_install_lists_every_fault_once(cluster)
            except Exception as error:
                caught = error
                preflight.print_failure(error)
        self.assertEqual(attempted, list(preflight.RESTORATION_LABELS))
        if original_failure is AssertionError:
            original = caught.__context__ if failed_labels else caught
            self.assertIsInstance(original, AssertionError)
            self.assertIn("must not leave a release", str(original))
        if failed_labels:
            self.assertIsInstance(caught, ExceptionGroup)
            self.assertEqual([group.message for group in caught.exceptions], list(failed_labels))
            self.assertEqual([group.exceptions[0] for group in caught.exceptions], list(errors.values()))
            self.assertIs(caught.__context__, original if original_failure else None)
            for label in failed_labels:
                self.assertIn(f"restoration: {label}", output.getvalue())
        elif original_failure:
            self.assertIs(caught, original)
        else:
            self.assertIsNone(caught)
        if original_failure:
            self.assertIn(original_failure.__name__, output.getvalue())
            self.assertIn("test-preflight-kind.py:", output.getvalue())
            self.assertIsNotNone(original.__traceback__)
        self.assertNotIn(SENTINEL, output.getvalue())


class DiagnosticTests(unittest.TestCase):
    def test_report_summary_is_bounded_and_allowlisted(self):
        """Actual parser/schema failures and arbitrary fields never become diagnostic output."""
        for variant in ("valid", "missing", "json", "schema", "unknown id", "unknown status", "extra field", "success"):
            with self.subTest(variant=variant):
                report = report_fixture()
                entry = report["results"][0]
                if variant in ("unknown id", "unknown status"):
                    entry["id" if variant == "unknown id" else "status"] = SENTINEL
                if variant == "schema":
                    report["release"]["revision"] = SENTINEL
                if variant == "extra field":
                    report[SENTINEL] = SENTINEL
                report["results"] *= 100
                payload = json.dumps(report) if variant != "json" else "{" + SENTINEL
                stderr = SENTINEL if variant == "missing" else f"OSAC-PREFLIGHT-JSON v1: {payload}\n{SENTINEL}"
                proc = subprocess.CompletedProcess([], 0 if variant == "success" else 1, SENTINEL, stderr)
                summary = preflight.helm_diagnostics(proc)
                self.assertNotIn(SENTINEL, summary)
                self.assertLess(len(summary), 200)
                if variant == "valid":
                    self.assertEqual(summary, "Helm exit code: 1; checks: cert-manager-crds=fail")
                elif variant == "unknown id":
                    self.assertIn("no known checks", summary)
                else:
                    self.assertIn("preflight report unavailable", summary)

    def test_runner_suppresses_arbitrary_error_payloads_in_all_phases(self):
        """Cases, setup and teardown expose only safe types/frames, including chained groups."""

        def failure(kind, *args):
            try:
                if kind == "command":
                    cluster = object.__new__(preflight.Cluster)
                    with patch.object(
                        preflight.subprocess, "run", return_value=subprocess.CompletedProcess([], 1, SENTINEL, SENTINEL)
                    ):
                        cluster.run("helm", SENTINEL)
                elif kind == "timeout":
                    raise subprocess.TimeoutExpired(SENTINEL, 1, output=SENTINEL, stderr=SENTINEL)
                elif kind in ("missing", "json", "schema", "success"):
                    report = report_fixture()
                    report["release"]["revision"] = SENTINEL
                    payload = "{" + SENTINEL if kind == "json" else json.dumps(report)
                    stderr = SENTINEL if kind == "missing" else f"OSAC-PREFLIGHT-JSON v1: {payload}"
                    preflight.failure_report(
                        subprocess.CompletedProcess([], 0 if kind == "success" else 1, SENTINEL, stderr)
                    )
                else:
                    raise type(SENTINEL, (Exception,), {})(SENTINEL)
            except Exception as original:
                original.add_note(SENTINEL)
                raise ExceptionGroup(SENTINEL, [original, ValueError(SENTINEL)])

        for phase in ("case", "setup", "teardown", "setup and teardown"):
            for kind in ("command", "missing", "json", "schema", "success", "timeout", "custom"):
                with self.subTest(phase=phase, kind=kind):
                    cluster = Mock(spec=preflight.Cluster, ns="test")
                    output = io.StringIO()
                    with (
                        patch.multiple(
                            preflight,
                            Cluster=Mock(return_value=cluster),
                            create_prerequisites=Mock(side_effect=partial(failure, kind) if "setup" in phase else None),
                            cleanup=Mock(side_effect=partial(failure, kind) if "teardown" in phase else None),
                            CASES=[lambda cluster: failure(kind)] if phase == "case" else [],
                        ),
                        patch.object(preflight.sys, "argv", [str(SCRIPT), "--context", "kind-test"]),
                        redirect_stdout(output),
                    ):
                        self.assertEqual(preflight.main(), 1)
                    self.assertIn("ExceptionGroup", output.getvalue())
                    self.assertNotIn(SENTINEL, output.getvalue())

    def test_helm_unit_checkout_uses_callers_fixed_commit(self):
        """The real reusable workflow consumes the caller's requested ref without credentials."""
        workflows = SCRIPT.parents[2] / ".github/workflows"
        workflow = preflight.yaml.safe_load((workflows / "tests.yml").read_text())
        steps = [step for job in workflow["jobs"].values() for step in job.get("steps", [])]
        checkout = next(
            step
            for step in steps
            if step.get("uses", "").startswith("actions/checkout@") and step.get("if") == "inputs.kind == 'helm-unit'"
        )
        self.assertEqual(checkout["with"], {"ref": "${{ inputs.commit }}", "persist-credentials": False})
        caller = preflight.yaml.safe_load((workflows / "unit-tests.yml").read_text())
        self.assertEqual(caller["jobs"]["tests"]["with"]["commit"], "${{ inputs.commit || '' }}")


if __name__ == "__main__":
    unittest.main()
